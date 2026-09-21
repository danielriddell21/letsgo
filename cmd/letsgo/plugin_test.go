package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/selfupdate"
)

func TestSplitPluginRef(t *testing.T) {
	for _, tc := range []struct{ ref, name, version string }{
		{"letsgo-multi", "letsgo-multi", ""},
		{"letsgo-multi@v0.2.0", "letsgo-multi", "v0.2.0"},
		{"letsgo-multi@latest", "letsgo-multi", "latest"},
		{"", "", ""},
	} {
		name, version := splitPluginRef(tc.ref)
		if name != tc.name || version != tc.version {
			t.Errorf("splitPluginRef(%q) = %q, %q; want %q, %q",
				tc.ref, name, version, tc.name, tc.version)
		}
	}
}

// The line printed after an install is the one that goes into letsgo.mod, so
// it has to be copy-pasteable when the hook is known.
func TestDescribePinFillsTheHookFromTheConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")

	var out bytes.Buffer
	describePin(&out, "letsgo-multi", &selfupdate.Update{
		Version:      "0.2.0",
		BinarySHA256: strings.Repeat("b", 64),
	})

	want := "plugin archive-layout letsgo-multi v0.2.0 sha256:" + strings.Repeat("b", 64)
	if !strings.Contains(out.String(), want) {
		t.Errorf("describePin printed:\n%s\nwant a line %q", out.String(), want)
	}
	if strings.Contains(out.String(), "<hook>") {
		t.Error("the hook was in the config and should not have been left blank")
	}
}

// letsgo does not guess a hook it has not been told. A placeholder the user
// has to fill is honest; a guessed hook that is wrong is a release nobody can
// account for.
func TestDescribePinLeavesAnUnknownHookBlank(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n")

	var out bytes.Buffer
	describePin(&out, "letsgo-env", &selfupdate.Update{
		Version:      "0.2.0",
		BinarySHA256: strings.Repeat("c", 64),
	})

	if !strings.Contains(out.String(), "plugin <hook> letsgo-env v0.2.0") {
		t.Errorf("describePin printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "the plugin documents") {
		t.Error("an unfilled hook should say where to find it")
	}
}

// Installing a plugin in a directory that has no letsgo.mod is a normal thing
// to do, and must not fail.
func TestPinnedHookToleratesNoConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	if hook := pinnedHook("letsgo-multi"); hook != "" {
		t.Errorf("pinnedHook = %q, want empty", hook)
	}
}

func TestPluginStatusReportsAMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, executable("letsgo-fake"))
	write(t, path, "the wrong bytes")

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	status, ok := pluginStatus(config.Plugin{
		Hook: "ldflags", Command: "letsgo-fake",
		Version: "v0.1.0", Digest: "sha256:" + strings.Repeat("d", 64),
	})
	if ok {
		t.Fatal("a plugin whose digest differs from the pin is not ok")
	}
	if !strings.Contains(status, "dddddddddddd") {
		t.Errorf("status = %q, want it to name the pinned digest", status)
	}
}

func TestPluginStatusReportsAMissingPlugin(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	status, ok := pluginStatus(config.Plugin{
		Hook: "ldflags", Command: "letsgo-absent",
		Version: "v0.1.0", Digest: "sha256:" + strings.Repeat("e", 64),
	})
	if ok || !strings.Contains(status, "not installed") {
		t.Errorf("status = %q, ok = %v", status, ok)
	}
}

// An interrupted install must leave the old plugin, never half of a new one,
// so the write goes through a rename.
func TestWriteExecutableReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "letsgo-multi")
	write(t, path, "old")

	if err := writeExecutable(path, []byte("new")); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}

	// The temporary file is written beside the target; none may survive.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".letsgo-plugin-") {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

// executable names a file LookPath will find: on Windows that means one of
// the extensions PATHEXT lists, and a bare name is not a program.
func executable(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
