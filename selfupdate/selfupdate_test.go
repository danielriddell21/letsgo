package selfupdate_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// forge serves a release the way GitHub does, so the package is exercised
// against the shape it actually meets.
type forge struct {
	tag      string
	manifest []byte
	archives map[string][]byte
	server   *httptest.Server
}

func newForge(t *testing.T, tag string) *forge {
	t.Helper()
	f := &forge{tag: tag, archives: map[string][]byte{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/you/tool/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		var assets []map[string]string
		// Only a release published by letsgo carries a manifest.
		if f.manifest != nil {
			assets = append(assets, map[string]string{
				"name":                 manifest.FileName,
				"browser_download_url": f.server.URL + "/download/" + manifest.FileName,
			})
		}
		for name := range f.archives {
			assets = append(assets, map[string]string{
				"name":                 name,
				"browser_download_url": f.server.URL + "/download/" + name,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag,
			"body":     "### Features\n\n- something",
			"html_url": "https://example.test/releases/" + f.tag,
			"assets":   assets,
		})
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/download/")
		if name == manifest.FileName {
			_, _ = w.Write(f.manifest)
			return
		}
		content, ok := f.archives[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(content)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// publish builds a release: one archive holding one binary, described by a
// manifest that records both digests.
func (f *forge) publish(t *testing.T, version, binary, content string) {
	t.Helper()

	name := fmt.Sprintf("tool_%s_linux_amd64.tar.gz", version)
	archive := tarGz(t, binary, content)
	f.archives[name] = archive

	m := &manifest.Manifest{
		Schema: manifest.Schema, Project: "tool", Version: version, Tag: f.tag,
		Artifacts: []manifest.Artifact{{
			Name: name, OS: "linux", Arch: "amd64", Binary: binary,
			SHA256:       sum(archive),
			BinarySHA256: sum([]byte(content)),
		}},
	}
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = data
}

func (f *forge) options(current string) selfupdate.Options {
	return selfupdate.Options{
		Repo: "you/tool", Current: current,
		APIEndpoint: f.server.URL,
		OS:          "linux", Arch: "amd64",
	}
}

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	body := []byte(content)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestCheckFindsANewerRelease(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "new binary bytes")

	update, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("no update offered")
	}

	if update.Version != "1.3.0" || update.Tag != "v1.3.0" {
		t.Errorf("update = %+v", update)
	}
	if update.Binary != "tool" || !strings.HasSuffix(update.Archive, "linux_amd64.tar.gz") {
		t.Errorf("update names %q in %q", update.Binary, update.Archive)
	}
	// A program should be able to show what changed before replacing itself.
	if !strings.Contains(update.Notes, "something") {
		t.Errorf("notes = %q", update.Notes)
	}
}

// The common case, and deliberately not an error.
func TestCheckReportsNothingWhenCurrent(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "bytes")

	for _, current := range []string{"1.3.0", "v1.3.0", "1.4.0"} {
		update, err := selfupdate.Check(context.Background(), f.options(current))
		if err != nil {
			t.Fatalf("Check(%q): %v", current, err)
		}
		if update != nil {
			t.Errorf("Check(%q) offered %s", current, update.Version)
		}
	}
}

// A development build has no claim to being ahead of a release.
func TestCheckTreatsAnUnparseableVersionAsOld(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "bytes")

	update, err := selfupdate.Check(context.Background(), f.options("dev"))
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("a dev build should be offered the release")
	}
}

func TestApplyToReplacesTheBinary(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "new binary bytes")

	update, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, []byte("old binary bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	if err := update.ApplyTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary bytes" {
		t.Errorf("installed %q", got)
	}

	// An updater that installs something the user cannot run has not updated
	// anything — everywhere the distinction exists. Windows has no execute
	// bit, so there is nothing to assert there.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("the installed binary is not executable: %v", info.Mode())
		}
	}
}

// The whole point: a substituted archive must not be installed.
func TestDownloadRefusesATamperedArchive(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "new binary bytes")

	update, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	// Swap the archive after the manifest was published, as an attacker with
	// write access to the release page would.
	f.archives[update.Archive] = tarGz(t, "tool", "malicious bytes")

	_, err = update.Download(context.Background())
	if err == nil {
		t.Fatal("a substituted archive was accepted")
	}
	if !strings.Contains(err.Error(), "refusing to install") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// A correct archive containing the wrong binary is a thing that can happen,
// which is why the manifest records both digests.
func TestDownloadRefusesAWrongBinaryInACorrectArchive(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "new binary bytes")

	// Re-point the manifest's binary digest at something else, leaving the
	// archive digest correct.
	var m manifest.Manifest
	if err := json.Unmarshal(f.manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.Artifacts[0].BinarySHA256 = sum([]byte("a different binary"))
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = data

	update, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	if _, err := update.Download(context.Background()); err == nil {
		t.Fatal("the archive's contents were not checked")
	}
}

func TestCheckReportsAMissingPlatform(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "bytes")

	o := f.options("1.2.0")
	o.OS, o.Arch = "plan9", "riscv64"

	if _, err := selfupdate.Check(context.Background(), o); err == nil {
		t.Fatal("want an error for a platform the release does not build")
	}
}

// Only releases published by letsgo carry a manifest, and without one there is
// nothing describing what to download.
func TestCheckReportsAReleaseWithNoManifest(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.manifest = nil
	f.archives["tool_1.3.0_linux_amd64.tar.gz"] = []byte("x")

	_, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err == nil || !strings.Contains(err.Error(), manifest.FileName) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckNeedsARepository(t *testing.T) {
	if _, err := selfupdate.Check(context.Background(), selfupdate.Options{}); err == nil {
		t.Fatal("want an error without a repository")
	}
}

// zipped builds a Windows-shaped release archive.
func zipped(t *testing.T, name, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// publishZip publishes a Windows release: a zip, and a binary with the .exe
// the manifest does not record but the archive carries.
func (f *forge) publishZip(t *testing.T, version string, content string) {
	t.Helper()

	name := fmt.Sprintf("tool_%s_windows_amd64.zip", version)
	archive := zipped(t, "tool.exe", content)
	f.archives[name] = archive

	m := &manifest.Manifest{
		Schema: manifest.Schema, Project: "tool", Version: version, Tag: f.tag,
		Artifacts: []manifest.Artifact{{
			Name: name, OS: "windows", Arch: "amd64", Binary: "tool",
			SHA256:       sum(archive),
			BinarySHA256: sum([]byte(content)),
		}},
	}
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = data
}

// The Windows path has its own archive format and its own executable suffix,
// and neither is exercised by the tar.gz case.
func TestWindowsReleaseIsAZip(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publishZip(t, "1.3.0", "new windows bytes")

	o := f.options("1.2.0")
	o.OS, o.Arch = "windows", "amd64"

	update, err := selfupdate.Check(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("no update offered")
	}
	if update.Binary != "tool.exe" {
		t.Errorf("binary = %q, want tool.exe", update.Binary)
	}

	binary, err := update.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "new windows bytes" {
		t.Errorf("downloaded %q", binary)
	}
}

func TestZipWithoutTheBinaryIsReported(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publishZip(t, "1.3.0", "bytes")

	// An archive whose digest is right but whose contents are not what the
	// manifest names.
	replacement := zipped(t, "something-else.exe", "bytes")
	var m manifest.Manifest
	if err := json.Unmarshal(f.manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.Artifacts[0].SHA256 = sum(replacement)
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = data
	f.archives[m.Artifacts[0].Name] = replacement

	o := f.options("1.2.0")
	o.OS, o.Arch = "windows", "amd64"

	update, err := selfupdate.Check(context.Background(), o)
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	_, err = update.Download(context.Background())
	if err == nil || !strings.Contains(err.Error(), "contains no tool.exe") {
		t.Fatalf("err = %v", err)
	}
}

func TestTarGzWithoutTheBinaryIsReported(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "not-the-binary", "bytes")

	update, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	// The manifest names a binary the archive does not hold.
	update.Binary = "tool"
	if _, err := update.Download(context.Background()); err == nil {
		t.Fatal("a missing binary was not reported")
	}
}

// A forge that is down says nothing about the release, and must not look like
// a corrupt one.
func TestCheckReportsAForgeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, err := selfupdate.Check(context.Background(), selfupdate.Options{
		Repo: "you/tool", Current: "1.0.0", APIEndpoint: server.URL, OS: "linux", Arch: "amd64",
	})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckReportsAnEmptyRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	_, err := selfupdate.Check(context.Background(), selfupdate.Options{
		Repo: "you/tool", Current: "1.0.0", APIEndpoint: server.URL, OS: "linux", Arch: "amd64",
	})
	if err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("err = %v", err)
	}
}

// The archive is described but not attached: a release page that was edited
// after publication.
func TestCheckReportsAMissingArchive(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "bytes")
	f.archives = map[string][]byte{} // described by the manifest, no longer attached

	_, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err == nil || !strings.Contains(err.Error(), "does not attach") {
		t.Fatalf("err = %v", err)
	}
}

// Writing into a directory that does not exist is the shape of "installed
// somewhere this process cannot write", and has to say so rather than panic.
func TestApplyToReportsAnUnwritableTarget(t *testing.T) {
	f := newForge(t, "v1.3.0")
	f.publish(t, "1.3.0", "tool", "bytes")

	update, err := selfupdate.Check(context.Background(), f.options("1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	target := filepath.Join(t.TempDir(), "no-such-directory", "tool")
	if err := update.ApplyTo(context.Background(), target); err == nil {
		t.Fatal("want an error for an unwritable target")
	}
}
