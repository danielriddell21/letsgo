package release_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/release"
)

func sumOf(body string) string {
	s := sha256.Sum256([]byte(body))
	return hex.EncodeToString(s[:])
}

func writeFiles(t *testing.T, dir string, files map[string]string) []string {
	t.Helper()
	var names []string
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// Every file the release publishes has a digest, SHA256SUMS, the SBOM and
// install.sh included, so an upload is never compared by size alone.
func TestDigestsCoverEveryPublishedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{"a.tar.gz": "a", build.ChecksumFile: "sums", "foo.spdx.json": "sbom", "install.sh": "#!/bin/sh"}
	r := &release.Result{Dir: dir, Files: writeFiles(t, dir, files)}

	got, err := r.Digests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(files) {
		t.Fatalf("digests = %v, want %d files", got, len(files))
	}
	for name, body := range files {
		if got[name] != sumOf(body) {
			t.Errorf("%s = %q, want %q", name, got[name], sumOf(body))
		}
	}
}

func TestDigestsNameAMissingFile(t *testing.T) {
	t.Parallel()
	r := &release.Result{Dir: t.TempDir(), Files: []string{"absent.tar.gz"}}
	if _, err := r.Digests(); err == nil {
		t.Error("Digests of a missing file succeeded")
	}
}

// A stamp changes the manifest; Restamp is what keeps SHA256SUMS honest about
// it, and about every other file, without the stamper hashing anything.
func TestRestampRewritesTheManifestAndTheChecksums(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	names := writeFiles(t, dir, map[string]string{"a.tar.gz": "a", manifest.FileName: "{}"})
	r := &release.Result{Dir: dir, Manifest: &manifest.Manifest{}, Files: append(names, build.ChecksumFile)}

	if err := r.Restamp(); err != nil {
		t.Fatal(err)
	}

	sums, err := os.ReadFile(filepath.Join(dir, build.ChecksumFile))
	if err != nil {
		t.Fatal(err)
	}
	manifestSum, err := r.Digest(manifest.FileName)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{sumOf("a") + "  a.tar.gz", manifestSum + "  " + manifest.FileName} {
		if !strings.Contains(string(sums), want) {
			t.Errorf("%s = %q, want a line %q", build.ChecksumFile, sums, want)
		}
	}
	if strings.Contains(string(sums), build.ChecksumFile) {
		t.Errorf("%s lists itself: %q", build.ChecksumFile, sums)
	}
}
