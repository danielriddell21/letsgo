package zig_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/zig"
)

// darwin is the case worth asserting: zig carries libc for macOS but not
// Apple's frameworks, and Go's darwin runtime links CoreFoundation, so a cgo
// build for darwin needs a macOS host. Reporting that is better than a link
// error nobody can read.
func TestTargetCoversWhatZigCanCrossCompile(t *testing.T) {
	for _, c := range []struct {
		target gobuild.Target
		want   string
	}{
		{gobuild.Target{OS: "linux", Arch: "amd64"}, "x86_64-linux-musl"},
		{gobuild.Target{OS: "linux", Arch: "arm64"}, "aarch64-linux-musl"},
		{gobuild.Target{OS: "windows", Arch: "amd64"}, "x86_64-windows-gnu"},
	} {
		got, ok := zig.Target(c.target)
		if !ok || got != c.want {
			t.Errorf("Target(%s) = %q, %v; want %q", c.target, got, ok, c.want)
		}
	}

	for _, target := range []gobuild.Target{
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	} {
		if _, ok := zig.Target(target); ok {
			t.Errorf("Target(%s) reports it can be cross-compiled, which it cannot", target)
		}
	}
}

func TestVersionsIncludesTheDefault(t *testing.T) {
	for _, v := range zig.Versions() {
		if v == zig.Default {
			return
		}
	}
	t.Errorf("the default %s is not among the known versions %v", zig.Default, zig.Versions())
}

// An unknown version is refused rather than downloaded unverified: the pin is
// the whole reason the compiler is a recorded input.
func TestEnsureRefusesAnUnpinnedVersion(t *testing.T) {
	_, err := zig.Ensure(context.Background(), "0.0.1-nope", "", t.TempDir())
	if err == nil {
		t.Fatal("an unknown version with no digest should not have been fetched")
	}
	if !strings.Contains(err.Error(), "no digest for") {
		t.Errorf("error = %q", err)
	}
}

// The real download, which is 50MB and needs the network, so it runs only when
// asked for. CI runs it in the job that proves cgo reproduces.
func TestEnsureDownloadsAndVerifies(t *testing.T) {
	if os.Getenv("LETSGO_ZIG_DOWNLOAD") == "" {
		t.Skip("set LETSGO_ZIG_DOWNLOAD=1 to exercise the real download")
	}

	dir := t.TempDir()
	tc, err := zig.Ensure(context.Background(), "", "", dir)
	if err != nil {
		t.Fatal(err)
	}

	if tc.Version != zig.Default {
		t.Errorf("version = %q", tc.Version)
	}
	if !strings.HasPrefix(tc.Digest, "sha256:") || len(tc.Digest) != len("sha256:")+64 {
		t.Errorf("digest = %q", tc.Digest)
	}
	if _, err := os.Stat(tc.Path); err != nil {
		t.Fatalf("the executable is not where Ensure said: %v", err)
	}

	cc, ok := tc.CC(gobuild.Target{OS: "linux", Arch: "arm64"})
	if !ok || !strings.HasSuffix(cc, "cc -target aarch64-linux-musl -mcpu=baseline") {
		t.Errorf("CC = %q", cc)
	}

	// The second call must reuse what the first extracted rather than
	// downloading 50MB again.
	again, err := zig.Ensure(context.Background(), "", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != tc.Path {
		t.Errorf("a second Ensure produced %q, not the cached %q", again.Path, tc.Path)
	}
}

// A digest that does not match what was downloaded must stop the release.
func TestEnsureRefusesAMismatchedDigest(t *testing.T) {
	if os.Getenv("LETSGO_ZIG_DOWNLOAD") == "" {
		t.Skip("set LETSGO_ZIG_DOWNLOAD=1 to exercise the real download")
	}

	wrong := "sha256:" + strings.Repeat("0", 64)
	_, err := zig.Ensure(context.Background(), zig.Default, wrong, t.TempDir())
	if err == nil {
		t.Fatal("a mismatched digest should not have been accepted")
	}
	if !strings.Contains(err.Error(), "the pin says") {
		t.Errorf("error = %q", err)
	}
}
