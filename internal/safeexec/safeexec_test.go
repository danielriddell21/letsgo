package safeexec

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The directories most likely to be user-owned are the ones most worth
// excluding, and the ones most likely to be added back by someone who has not
// read why.
func TestSystemDirsExcludeUserWritableLocations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	forbidden := []string{"/usr/local/bin", "/usr/local/sbin", "/opt/homebrew/bin", ".", ""}

	for _, dir := range SystemDirs() {
		for _, bad := range forbidden {
			if dir == bad {
				t.Errorf("%q is writable without elevation and must not be searched", dir)
			}
		}
		if !filepath.IsAbs(dir) {
			t.Errorf("%q is not absolute", dir)
		}
	}
}

// Extras exist for programs whose location is legitimately variable, but they
// must not be able to shadow a system directory.
func TestFixedPathAppendsExtrasAfterSystemDirs(t *testing.T) {
	path := FixedPath("/opt/toolchain/bin")
	dirs := strings.Split(path, string(os.PathListSeparator))

	if dirs[len(dirs)-1] != "/opt/toolchain/bin" {
		t.Errorf("the extra directory is not last: %v", dirs)
	}
	if len(dirs) != len(SystemDirs())+1 {
		t.Errorf("got %d entries, want %d", len(dirs), len(SystemDirs())+1)
	}
	// An empty extra would add a "" entry, which the shell reads as the
	// current directory.
	if strings.Contains(FixedPath(""), string(os.PathListSeparator)+string(os.PathListSeparator)) {
		t.Error("an empty extra produced an empty PATH entry")
	}
}

// A child handed the caller's PATH can be redirected exactly as the caller
// could have been.
func TestEnvWithFixedPathReplacesEveryPathEntry(t *testing.T) {
	env := []string{"HOME=/home/someone", "PATH=/tmp/attacker", "Path=/tmp/attacker-too", "GOFLAGS=-x"}

	got := EnvWithFixedPath(env)

	var paths []string
	preserved := map[string]bool{}
	for _, entry := range got {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			paths = append(paths, value)
			continue
		}
		preserved[entry] = true
	}

	if len(paths) != 1 {
		t.Fatalf("got %d PATH entries, want exactly 1: %v", len(paths), paths)
	}
	if strings.Contains(paths[0], "attacker") {
		t.Errorf("an inherited PATH survived: %q", paths[0])
	}
	// Everything else must survive: HOME in particular decides which
	// configuration a tool reads.
	if !preserved["HOME=/home/someone"] || !preserved["GOFLAGS=-x"] {
		t.Errorf("unrelated variables were dropped: %v", got)
	}
}

func TestLookIn(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "thing")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	found, err := LookIn([]string{t.TempDir(), dir}, "thing")
	if err != nil {
		t.Fatalf("LookIn: %v", err)
	}
	if found != tool {
		t.Errorf("found %q, want %q", found, tool)
	}

	if _, err := LookIn([]string{t.TempDir()}, "absent"); err == nil {
		t.Error("LookIn found something that is not there")
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()

	runnable := filepath.Join(dir, "runnable")
	if err := os.WriteFile(runnable, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsExecutable(runnable) {
		t.Error("an executable file was not recognised")
	}

	if IsExecutable(filepath.Join(dir, "absent")) {
		t.Error("a missing file was reported as executable")
	}
	if IsExecutable(dir) {
		t.Error("a directory was reported as executable")
	}

	if runtime.GOOS != "windows" {
		plain := filepath.Join(dir, "plain")
		if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if IsExecutable(plain) {
			t.Error("a non-executable file was reported as executable")
		}
	}
}
