package sumdb

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// buildZip makes an in-memory module zip, keyed by path relative to the
// module@version/ prefix, and returns its bytes alongside the real h1: hash
// so tests can assert both fetchZip's input and hashZip's output.
func buildZip(t *testing.T, modulePath, version string, files map[string]string) ([]byte, string) {
	t.Helper()

	prefix := modulePath + "@" + version + "/"
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	type entry struct {
		name string
		sum  [sha256.Size]byte
	}
	entries := make([]entry, 0, len(names))
	for _, name := range names {
		content := files[name]
		full := prefix + name
		w, err := zw.Create(full)
		if err != nil {
			t.Fatalf("buildZip: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("buildZip: %v", err)
		}
		entries = append(entries, entry{name: full, sum: sha256.Sum256([]byte(content))})
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("buildZip: %v", err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%x  %s\n", e.sum, e.name)
	}
	h1 := "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil))

	return buf.Bytes(), h1
}

// buildArchive writes a tar.gz source archive (as internal/build.WriteSource
// produces) under a single top-level directory, and returns its path.
func buildArchive(t *testing.T, dir, topLevel string, files map[string]string) string {
	t.Helper()

	path := filepath.Join(dir, "source.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("buildArchive: %v", err)
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		content := files[name]
		hdr := &tar.Header{
			Name: topLevel + "/" + name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("buildArchive: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("buildArchive: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("buildArchive: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("buildArchive: %v", err)
	}
	return path
}

// fakeServers starts a sumdb server (serving the lookup record for one
// module@version) and a proxy server (serving zipData for the same), and
// returns their base URLs.
func fakeServers(t *testing.T, modulePath, version, sumH1 string, zipData []byte) (sumdbURL, proxyURL string) {
	t.Helper()

	sumdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s %s\n%s %s/go.mod h1:irrelevant=\n\n-- signature --\n", modulePath, version, sumH1, modulePath, version)
	}))
	t.Cleanup(sumdbServer.Close)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipData)
	}))
	t.Cleanup(proxyServer.Close)

	return sumdbServer.URL, proxyServer.URL
}

func TestHashZipMatchesARealModule(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "quote.zip"))
	if err != nil {
		t.Fatal(err)
	}

	files, h1, err := hashZip(data, "rsc.io/quote", "v1.5.2")
	if err != nil {
		t.Fatal(err)
	}

	const want = "h1:w5fcysjrx7yqtD/aO+QwRjYZOKnaM9Uh2b40tElTs3Y="
	if h1 != want {
		t.Errorf("hashZip h1 = %q, want %q", h1, want)
	}
	if len(files) == 0 {
		t.Error("hashZip returned no files relative to the module@version/ prefix")
	}
	if _, ok := files["go.mod"]; !ok {
		t.Errorf("files = %v, want a go.mod entry stripped of its module@version/ prefix", files)
	}
}

func TestCheckReportsAMatch(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	files := map[string]string{"go.mod": "module example.com/foo\n", "foo.go": "package foo\n"}
	zipData, h1 := buildZip(t, modulePath, version, files)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, h1, zipData)
	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", files)

	result, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, version, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched {
		t.Errorf("Check() = %+v, want a match", result)
	}
	if result.SumH1 != h1 || result.ZipH1 != h1 {
		t.Errorf("Check() hashes = %q/%q, want both %q", result.SumH1, result.ZipH1, h1)
	}
}

func TestCheckReportsAMismatchedFile(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	zipFiles := map[string]string{"go.mod": "module example.com/foo\n", "foo.go": "package foo\n"}
	zipData, h1 := buildZip(t, modulePath, version, zipFiles)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, h1, zipData)

	archiveFiles := map[string]string{"go.mod": "module example.com/foo\n", "foo.go": "package foo\n// tampered\n"}
	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", archiveFiles)

	result, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, version, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched {
		t.Error("Check() matched a tampered archive")
	}
	if len(result.Mismatched) != 1 || result.Mismatched[0] != "foo.go" {
		t.Errorf("Mismatched = %v, want [foo.go]", result.Mismatched)
	}
}

func TestCheckReportsAMissingFile(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	zipFiles := map[string]string{"go.mod": "module example.com/foo\n", "foo.go": "package foo\n"}
	zipData, h1 := buildZip(t, modulePath, version, zipFiles)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, h1, zipData)

	archiveFiles := map[string]string{"go.mod": "module example.com/foo\n"}
	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", archiveFiles)

	result, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, version, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched {
		t.Error("Check() matched an archive missing a zip file")
	}
	if len(result.Missing) != 1 || result.Missing[0] != "foo.go" {
		t.Errorf("Missing = %v, want [foo.go]", result.Missing)
	}
}

func TestCheckReportsExtraArchiveFilesWithoutFailing(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	zipFiles := map[string]string{"go.mod": "module example.com/foo\n"}
	zipData, h1 := buildZip(t, modulePath, version, zipFiles)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, h1, zipData)

	archiveFiles := map[string]string{"go.mod": "module example.com/foo\n", "README.md": "docs\n"}
	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", archiveFiles)

	result, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, version, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched {
		t.Errorf("Check() = %+v, want a match — extra archive files are informational only", result)
	}
	if len(result.Extra) != 1 || result.Extra[0] != "README.md" {
		t.Errorf("Extra = %v, want [README.md]", result.Extra)
	}
}

func TestCheckReportsASumdbProxyDisagreement(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	files := map[string]string{"go.mod": "module example.com/foo\n"}
	zipData, zipH1 := buildZip(t, modulePath, version, files)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, "h1:disagrees==", zipData)
	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", files)

	result, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, version, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched {
		t.Error("Check() matched despite a sumdb/proxy hash disagreement")
	}
	if result.SumH1 != "h1:disagrees==" || result.ZipH1 != zipH1 {
		t.Errorf("Check() hashes = %q/%q, want %q/%q", result.SumH1, result.ZipH1, "h1:disagrees==", zipH1)
	}
}

func TestCheckFailsWhenTheArchiveIsMissing(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	files := map[string]string{"go.mod": "module example.com/foo\n"}
	zipData, h1 := buildZip(t, modulePath, version, files)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, h1, zipData)

	_, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, version, filepath.Join(t.TempDir(), "missing.tar.gz"))
	if err == nil {
		t.Fatal("Check succeeded with a missing archive")
	}
}

func TestCheckFailsWhenSumdbHasNoRecord(t *testing.T) {
	sumdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer sumdbServer.Close()

	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", map[string]string{"go.mod": "module example.com/foo\n"})

	_, err := Check(context.Background(), sumdbServer.URL, "https://proxy.invalid", "example.com/foo", "v1.0.0", archivePath)
	if err == nil {
		t.Fatal("Check succeeded despite a sumdb 404")
	}
}

func TestCheckFailsWhenTheProxyErrors(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	_, h1 := buildZip(t, modulePath, version, map[string]string{"go.mod": "module example.com/foo\n"})
	sumdbURL, _ := fakeServers(t, modulePath, version, h1, nil)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer proxyServer.Close()

	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", map[string]string{"go.mod": "module example.com/foo\n"})

	_, err := Check(context.Background(), sumdbURL, proxyServer.URL, modulePath, version, archivePath)
	if err == nil {
		t.Fatal("Check succeeded despite a proxy 500")
	}
}

func TestLookupHashFailsWithNoMatchingLine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "example.com/foo v0.9.0 h1:wrongversion==\n")
	}))
	defer server.Close()

	if _, err := lookupHash(context.Background(), server.URL, "example.com/foo", "v1.0.0"); err == nil {
		t.Fatal("lookupHash succeeded with no matching h1 line")
	}
}

func TestGetFailsOnANonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	if _, err := get(context.Background(), server.URL, 1<<10); err == nil {
		t.Fatal("get succeeded on a 418 response")
	}
}

func TestHashZipFailsOnCorruptData(t *testing.T) {
	if _, _, err := hashZip([]byte("not a zip"), "example.com/foo", "v1.0.0"); err == nil {
		t.Fatal("hashZip succeeded on corrupt data")
	}
}

func TestReadArchiveFailsOnCorruptData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.tar.gz")
	if err := os.WriteFile(path, []byte("not a gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(path); err == nil {
		t.Fatal("readArchive succeeded on corrupt data")
	}
}

func TestCheckNormalizesVersionPrefix(t *testing.T) {
	const modulePath, version = "example.com/foo", "v1.0.0"
	files := map[string]string{"go.mod": "module example.com/foo\n"}
	zipData, h1 := buildZip(t, modulePath, version, files)
	sumdbURL, proxyURL := fakeServers(t, modulePath, version, h1, zipData)
	archivePath := buildArchive(t, t.TempDir(), "foo-1.0.0", files)

	result, err := Check(context.Background(), sumdbURL, proxyURL, modulePath, "1.0.0", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched {
		t.Errorf("Check() with an unprefixed version = %+v, want a match", result)
	}
}
