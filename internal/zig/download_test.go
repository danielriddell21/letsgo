package zig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// serve stands in for ziglang.org, so the download path is exercised without
// fetching fifty megabytes. The digest is what decides whether the bytes are
// acceptable, and that check is the same wherever they came from.
func serve(t *testing.T, version, host string, body []byte) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+version+"/"+archiveName(version, host) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	previous := downloadBase
	downloadBase = server.URL
	t.Cleanup(func() { downloadBase = previous })

	return server.URL
}

func target(goos, goarch string) gobuild.Target {
	return gobuild.Target{OS: goos, Arch: goarch}
}

func digestOfBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestDownloadVerifiesAndExtracts(t *testing.T) {
	staging := t.TempDir()
	archive := tarball(t, staging, "zig-x86_64-linux-0.16.0")

	body, err := os.ReadFile(archive) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	serve(t, "0.16.0", "x86_64-linux", body)

	dest := filepath.Join(t.TempDir(), "0.16.0-abc")
	if err := download(context.Background(), "0.16.0", "x86_64-linux", digestOfBytes(body), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "zig")); err != nil {
		t.Errorf("the toolchain was not extracted: %v", err)
	}
}

// The pin is the whole point: bytes that do not match it are not the compiler
// the release recorded, whatever the server says.
func TestDownloadRefusesBytesThatDoNotMatchThePin(t *testing.T) {
	serve(t, "0.16.0", "x86_64-linux", []byte("not a zig tarball"))

	err := download(context.Background(), "0.16.0", "x86_64-linux",
		strings.Repeat("0", 64), filepath.Join(t.TempDir(), "dest"))

	if err == nil {
		t.Fatal("a mismatched download should have been refused")
	}
	if !strings.Contains(err.Error(), "the pin says") {
		t.Errorf("error = %q", err)
	}
}

func TestDownloadReportsAMissingRelease(t *testing.T) {
	serve(t, "0.16.0", "x86_64-linux", []byte("x"))

	err := download(context.Background(), "9.9.9", "x86_64-linux",
		strings.Repeat("0", 64), filepath.Join(t.TempDir(), "dest"))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v", err)
	}
}

// A toolchain already extracted is reused rather than fetched again: a release
// builds several targets, and downloading the compiler for each would be
// absurd.
func TestEnsureReusesWhatIsAlreadyThere(t *testing.T) {
	cache := t.TempDir()
	digest := strings.Repeat("a", 64)

	dir := filepath.Join(cache, "zig", "0.16.0-"+digest[:12])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "zig"+exeSuffix())
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	// No server is serving anything, so a fetch would fail.
	tc, err := Ensure(context.Background(), "0.16.0", "sha256:"+digest, cache)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Path != exe {
		t.Errorf("Path = %q, want the cached %q", tc.Path, exe)
	}
	if tc.Digest != "sha256:"+digest {
		t.Errorf("Digest = %q", tc.Digest)
	}
}

// The compiler command carries the target and a fixed instruction set, so that
// what gets compiled does not depend on the machine compiling it.
func TestCompilerCommandsPinTheTarget(t *testing.T) {
	tc := Toolchain{Path: "/cache/zig", Version: "0.16.0"}

	cc, ok := tc.CC(target("linux", "arm64"))
	if !ok || cc != "/cache/zig cc -target aarch64-linux-musl -mcpu=baseline" {
		t.Errorf("CC = %q, %v", cc, ok)
	}
	cxx, ok := tc.CXX(target("windows", "amd64"))
	if !ok || cxx != "/cache/zig c++ -target x86_64-windows-gnu -mcpu=baseline" {
		t.Errorf("CXX = %q, %v", cxx, ok)
	}
	if _, ok := tc.CC(target("darwin", "arm64")); ok {
		t.Error("darwin reports a compiler, but it needs a macOS host")
	}
}
