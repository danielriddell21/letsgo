package safeexec

import (
	"io/fs"
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

func TestSystemDirsOnWindowsUseSystemRootAndProgramFiles(t *testing.T) {
	env := map[string]string{"SystemRoot": `D:\Win`, "ProgramFiles": `D:\PF`, "ProgramW6432": `D:\PF64`}
	got := systemDirsFor("windows", func(k string) string { return env[k] })

	want := []string{
		filepath.Join(`D:\Win`, "system32"), `D:\Win`,
		filepath.Join(`D:\PF`, "Git", "cmd"), filepath.Join(`D:\PF`, "Git", "bin"),
		filepath.Join(`D:\PF64`, "Git", "cmd"), filepath.Join(`D:\PF64`, "Git", "bin"),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("dirs = %v, want %v", got, want)
	}
}

func TestSystemDirsOnWindowsDefaultTheRoot(t *testing.T) {
	got := systemDirsFor("windows", func(string) string { return "" })
	if len(got) != 2 || got[1] != `C:\Windows` {
		t.Errorf("dirs = %v, want the default root and its system32", got)
	}
}

func TestSystemDirsElsewhereAreTheFixedUnixOnes(t *testing.T) {
	got := systemDirsFor("linux", func(string) string { return "/tmp/attacker" })
	if strings.Join(got, ":") != "/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Errorf("dirs = %v", got)
	}
}

func TestRunnableOnEachPlatform(t *testing.T) {
	for _, tt := range []struct {
		goos string
		mode fs.FileMode
		want bool
	}{
		{"linux", 0o755, true},
		{"linux", 0o644, false},
		{"linux", 0o100, true},
		{"linux", fs.ModeDir | 0o755, false},
		{"windows", 0o644, true},
		{"windows", fs.ModeDir | 0o755, false},
		{"windows", fs.ModeSymlink | 0o777, false},
	} {
		if got := runnable(tt.goos, tt.mode); got != tt.want {
			t.Errorf("runnable(%s, %v) = %v, want %v", tt.goos, tt.mode, got, tt.want)
		}
	}
}

func TestExeSuffix(t *testing.T) {
	if got := exeFor("windows", "go"); got != "go.exe" {
		t.Errorf("windows: %q", got)
	}
	if got := exeFor("linux", "go"); got != "go" {
		t.Errorf("linux: %q", got)
	}
	if got := Exe("go"); got != exeFor(runtime.GOOS, "go") {
		t.Errorf("Exe = %q", got)
	}
}

func TestOverride(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "tool")
	if err := os.WriteFile(tool, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		value   string
		want    string // substring of the error, "" for success
		skipWin bool
	}{
		{"accepts an executable absolute path", tool, "", false},
		{"rejects a bare name", "git", "must be an absolute path", false},
		{"rejects a relative path", "./git", "must be an absolute path", false},
		{"rejects an empty value", "", "must be an absolute path", false},
		{"rejects a missing file", filepath.Join(dir, "absent"), "is not an executable file", false},
		{"rejects a directory", dir, "is not an executable file", false},
		{"rejects a non-executable file", plain, "is not an executable file", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipWin && runtime.GOOS == "windows" {
				t.Skip("windows has no execute bit")
			}
			got, err := Override("git", tt.value, "LETSGO_GIT")
			if tt.want == "" {
				if err != nil || got != tt.value {
					t.Fatalf("Override = %q, %v, want %q", got, err, tt.value)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to say %q", err, tt.want)
			}
			// The message must say what and where, so the user can fix it.
			for _, part := range []string{"git", "LETSGO_GIT"} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("err = %q, want it to name %q", err, part)
				}
			}
		})
	}
}
