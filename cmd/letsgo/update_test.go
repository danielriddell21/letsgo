package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// forge serves one release, enough for the updater to reach a decision.
func forge(t *testing.T, tag string, m *manifest.Manifest) string {
	t.Helper()

	var server *httptest.Server
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/you/tool/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		assets := []map[string]string{}
		if m != nil {
			assets = append(assets,
				map[string]string{"name": manifest.FileName, "browser_download_url": server.URL + "/manifest"},
			)
			for _, a := range m.Artifacts {
				assets = append(assets,
					map[string]string{"name": a.Name, "browser_download_url": server.URL + "/asset"})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag, "body": "notes", "html_url": "https://example.test/" + tag, "assets": assets,
		})
	})
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		data, err := m.Encode()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = w.Write(data)
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

func published(version string) *manifest.Manifest {
	return &manifest.Manifest{
		Schema: manifest.Schema, Project: "letsgo", Version: version,
		Artifacts: []manifest.Artifact{{
			Name: fmt.Sprintf("letsgo_%s_linux_amd64.tar.gz", version),
			OS:   "linux", Arch: "amd64", Binary: "letsgo",
			SHA256:       strings.Repeat("a", 64),
			BinarySHA256: strings.Repeat("b", 64),
		}},
	}
}

func updaterFor(t *testing.T, current, tag string, m *manifest.Manifest) (updater, *int) {
	t.Helper()

	installs := 0
	return updater{
		Options: selfupdate.Options{
			Repo: "you/tool", Current: current,
			APIEndpoint: forge(t, tag, m), OS: "linux", Arch: "amd64",
		},
		Current: current,
		Install: func(context.Context, *selfupdate.Update) error {
			installs++
			return nil
		},
	}, &installs
}

func TestUpdateInstallsANewerRelease(t *testing.T) {
	u, installs := updaterFor(t, "1.2.0", "v1.3.0", published("1.3.0"))

	var out bytes.Buffer
	if err := u.run(context.Background(), &out); err != nil {
		t.Fatal(err)
	}

	if *installs != 1 {
		t.Errorf("installed %d times, want 1", *installs)
	}
	got := out.String()
	for _, want := range []string{"1.3.0 is available", "aaaaaaaaaaaa", "bbbbbbbbbbbb", "installed"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

func TestUpdateSaysWhenCurrent(t *testing.T) {
	u, installs := updaterFor(t, "1.3.0", "v1.3.0", published("1.3.0"))

	var out bytes.Buffer
	if err := u.run(context.Background(), &out); err != nil {
		t.Fatal(err)
	}

	if *installs != 0 {
		t.Error("an update was installed over the latest release")
	}
	if !strings.Contains(out.String(), "is the latest release") {
		t.Errorf("output = %q", out.String())
	}
}

// --check reports and changes nothing. A self-replacing binary that installs
// when asked only to look is the worst possible bug in this file.
func TestUpdateCheckInstallsNothing(t *testing.T) {
	u, installs := updaterFor(t, "1.2.0", "v1.3.0", published("1.3.0"))
	u.Check = true

	var out bytes.Buffer
	if err := u.run(context.Background(), &out); err != nil {
		t.Fatal(err)
	}

	if *installs != 0 {
		t.Error("--check installed an update")
	}
	if !strings.Contains(out.String(), "1.3.0 is available") {
		t.Errorf("--check should still report:\n%s", out.String())
	}
}

func TestUpdateRespectsARefusal(t *testing.T) {
	u, installs := updaterFor(t, "1.2.0", "v1.3.0", published("1.3.0"))
	asked := ""
	u.Confirm = func(version string) bool {
		asked = version
		return false
	}

	var out bytes.Buffer
	if err := u.run(context.Background(), &out); err != nil {
		t.Fatal(err)
	}

	if asked != "1.3.0" {
		t.Errorf("asked about %q", asked)
	}
	if *installs != 0 {
		t.Error("an update was installed after a refusal")
	}
	if !strings.Contains(out.String(), "nothing was installed") {
		t.Errorf("output = %q", out.String())
	}
}

func TestUpdateInstallsAfterConsent(t *testing.T) {
	u, installs := updaterFor(t, "1.2.0", "v1.3.0", published("1.3.0"))
	u.Confirm = func(string) bool { return true }

	if err := u.run(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if *installs != 1 {
		t.Errorf("installed %d times, want 1", *installs)
	}
}

func TestUpdateReportsAFailedCheck(t *testing.T) {
	u, _ := updaterFor(t, "1.2.0", "v1.3.0", nil) // a release with no manifest

	if err := u.run(context.Background(), &bytes.Buffer{}); err == nil {
		t.Fatal("want an error when the release cannot be read")
	}
}

func TestUpdateReportsAFailedInstall(t *testing.T) {
	u, _ := updaterFor(t, "1.2.0", "v1.3.0", published("1.3.0"))
	u.Install = func(context.Context, *selfupdate.Update) error {
		return fmt.Errorf("disk full")
	}

	if err := u.run(context.Background(), &bytes.Buffer{}); err == nil {
		t.Fatal("a failed install was reported as success")
	}
}

func TestShortDigest(t *testing.T) {
	if got := short(strings.Repeat("a", 64)); got != strings.Repeat("a", 12) {
		t.Errorf("short = %q", got)
	}
	// Anything already short enough is left alone rather than padded.
	if got := short("abc"); got != "abc" {
		t.Errorf("short = %q", got)
	}
}
