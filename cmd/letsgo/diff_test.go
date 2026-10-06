package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/manifest"
)

// An unknown --format is rejected before the two sides are even resolved,
// so a typo doesn't cost a network round trip.
func TestRunDiffRejectsAnUnknownFormat(t *testing.T) {
	err := unwired.runDiff([]string{"--format", "yaml", "a.json", "b.json"})
	if err == nil || !strings.Contains(err.Error(), `unknown --format "yaml"`) {
		t.Fatalf("err = %v, want an unknown --format error", err)
	}
}

func TestRunDiffPrintsEachFormat(t *testing.T) {
	dir := t.TempDir()
	from := writeManifest(t, dir, "from.json", "v1.0.0", "go1.26.1")
	to := writeManifest(t, dir, "to.json", "v1.1.0", "go1.26.2")

	for _, tt := range []struct {
		format string
		want   string
	}{
		{"text", "v1.0.0 -> v1.1.0"},
		{"md", "go1.26.1 → go1.26.2"},
		{"json", `"schema": 1`},
	} {
		var runErr error
		out := captureStdout(t, func() {
			runErr = unwired.runDiff([]string{"--format", tt.format, from, to})
		})
		if runErr != nil {
			t.Fatalf("runDiff --format %s: %v", tt.format, runErr)
		}
		if !strings.Contains(out, tt.want) {
			t.Errorf("--format %s stdout = %q, want it to contain %q", tt.format, out, tt.want)
		}
	}
}

// Tags do not exist on disk, so the file system decides which side of a diff
// is which.
func TestIsManifestPath(t *testing.T) {
	if isManifestPath("") || isManifestPath("v1.2.3") {
		t.Error("a tag was mistaken for a path")
	}
	if isManifestPath(t.TempDir()) {
		t.Error("a directory was mistaken for a manifest")
	}

	path := t.TempDir() + "/letsgo.json"
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isManifestPath(path) {
		t.Error("a file was not recognised")
	}
}

// With no "to", diff compares against the latest release within the module's
// own scope, not another module's.
func TestRunDiffDefaultsToTheModulesOwnLatestRelease(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	if out, err := exec.Command("git", "-C", repoDir, "remote", "add", "origin", "https://github.com/you/foo.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/you/foo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "other/v9.0.0"})
	})
	mux.HandleFunc("/repos/you/foo/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "other/v9.0.0"}, {Name: "services/api/v1.2.3"}})
	})
	mux.HandleFunc("/repos/you/foo/releases/tags/services/api/v1.2.3", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{
			ID: 2, TagName: "services/api/v1.2.3",
			Assets: []github.Asset{{ID: 5, Name: manifest.FileName}},
		})
	})
	mux.HandleFunc("/repos/you/foo/releases/assets/5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schema":1,"version":"1.2.3"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f := forge{endpoint: srv.URL}

	t.Chdir(moduleDir)
	local := filepath.Join(t.TempDir(), "letsgo.json")
	if err := os.WriteFile(local, []byte(`{"schema":1,"version":"1.2.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.runDiff([]string{local}); err != nil {
		t.Fatalf("runDiff: %v", err)
	}
}
