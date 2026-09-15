package gobuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/safeexec"
)

// Whatever is found must be invoked by absolute path, so the choice cannot be
// re-made later in a different environment.
func TestToolchainResolvesAbsolutely(t *testing.T) {
	path, err := Toolchain()
	if err != nil {
		t.Fatalf("Toolchain: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("go resolved to %q, want an absolute path", path)
	}
	if !safeexec.IsExecutable(path) {
		t.Errorf("%q is not executable", path)
	}
}

// A subprocess given the caller's PATH can be redirected by a writable
// directory on it, and the toolchain execs plenty of its own helpers.
func TestEnvUsesAFixedPath(t *testing.T) {
	t.Setenv("PATH", "/tmp/attacker-controlled")

	var paths []string
	for _, entry := range Env(Host(), "", "", "") {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, "PATH") {
			paths = append(paths, value)
		}
	}

	if len(paths) != 1 {
		t.Fatalf("got %d PATH entries, want 1: %v", len(paths), paths)
	}
	if strings.Contains(paths[0], "attacker-controlled") {
		t.Errorf("the inherited PATH survived: %q", paths[0])
	}
	// The toolchain's own directory has to be reachable, or go cannot find
	// the helpers it ships with.
	if dir := toolchainDir(); dir != "" && !strings.Contains(paths[0], dir) {
		t.Errorf("PATH %q does not include the toolchain directory %q", paths[0], dir)
	}
}

// The Go toolchain is chosen by PATH: setup-go, gvm, asdf and mise all work
// that way, and many systems also carry an older Go in /usr/bin. Preferring a
// system copy would silently build with a different compiler than the one the
// user selected, which is how this once produced artifacts that differed
// between machines.
func TestToolchainFollowsPathNotSystemDirectories(t *testing.T) {
	t.Setenv("GOROOT", "")
	t.Setenv(ToolchainEnvOverride, "")

	onPath, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	wantAbs, err := filepath.Abs(onPath)
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveToolchain()
	if err != nil {
		t.Fatalf("resolveToolchain: %v", err)
	}
	if got != wantAbs {
		t.Errorf("resolved %q, but PATH selects %q; a system copy must not win", got, wantAbs)
	}
}

// GOROOT is the toolchain's own declaration of where it lives, so it outranks
// PATH.
func TestGorootOutranksPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, safeexec.Exe("go"))
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(ToolchainEnvOverride, "")
	t.Setenv("GOROOT", dir)

	got, err := resolveToolchain()
	if err != nil {
		t.Fatal(err)
	}
	if got != fake {
		t.Errorf("resolved %q, want the GOROOT copy %q", got, fake)
	}
}

func TestToolchainOverrideMustBeAbsoluteAndExecutable(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "go")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(ToolchainEnvOverride, good)
	if got, err := resolveToolchain(); err != nil || got != good {
		t.Errorf("resolveToolchain() = %q, %v; want %q", got, err, good)
	}

	// A bare name would be resolved through PATH by the operating system,
	// reintroducing exactly what this avoids.
	t.Setenv(ToolchainEnvOverride, "go")
	if _, err := resolveToolchain(); err == nil {
		t.Error("a bare name was accepted as an override")
	}

	t.Setenv(ToolchainEnvOverride, filepath.Join(dir, "absent"))
	if _, err := resolveToolchain(); err == nil {
		t.Error("a missing file was accepted as an override")
	}
}

// cgo is off unless a compiler is supplied, and supplying one turns it on
// together with CC. The pair matters: CGO_ENABLED=1 with no CC would fall back
// to the host's own compiler, which is the unrecorded input the whole design
// avoids.
func TestEnvEnablesCgoOnlyWithACompiler(t *testing.T) {
	off := map[string]string{}
	for _, entry := range Env(Host(), "", "", "") {
		if k, v, ok := strings.Cut(entry, "="); ok {
			off[k] = v
		}
	}
	if off["CGO_ENABLED"] != "0" {
		t.Errorf("CGO_ENABLED = %q with no compiler, want 0", off["CGO_ENABLED"])
	}
	if _, set := off["CC"]; set {
		t.Error("CC is set with no compiler supplied")
	}

	on := map[string]string{}
	for _, entry := range Env(Host(), "", "/zig cc -target x86_64-linux-musl", "/zig c++") {
		if k, v, ok := strings.Cut(entry, "="); ok {
			on[k] = v
		}
	}
	if on["CGO_ENABLED"] != "1" {
		t.Errorf("CGO_ENABLED = %q with a compiler, want 1", on["CGO_ENABLED"])
	}
	if on["CC"] != "/zig cc -target x86_64-linux-musl" {
		t.Errorf("CC = %q", on["CC"])
	}
	if on["CXX"] != "/zig c++" {
		t.Errorf("CXX = %q", on["CXX"])
	}
}
