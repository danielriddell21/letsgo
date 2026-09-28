package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// fakeHookPlugin writes a shell script that answers whatever body prints, and
// puts it on PATH under command, off the real machine's plugin store.
func fakeHookPlugin(t *testing.T, command, body string) (digest string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}
	t.Setenv(pluginstore.StoreEnvOverride, t.TempDir())

	dir := t.TempDir()
	path := filepath.Join(dir, command)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// A plugin never has to guess where its own config lives: config_dir is in
// every hook's input, pointing at .letsgo beside letsgo.mod.
func TestApplyLDFlagsPluginSendsConfigDir(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	t.Setenv("LETSGO_FAKE_INPUT", input)

	digest := fakeHookPlugin(t, "letsgo-env", `cat > "$LETSGO_FAKE_INPUT"; echo '{"ldflags":[]}'`)

	root := t.TempDir()
	p := &Plan{
		RootDir: root,
		Plugins: map[plugin.Hook]plugin.Plugin{
			plugin.HookLDFlags: {Hook: plugin.HookLDFlags, Command: "letsgo-env", Digest: digest},
		},
	}
	p.applyLDFlagsPlugin(context.Background())
	if !p.OK() {
		t.Fatalf("checks = %+v", p.Checks)
	}

	got, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `"config_dir":"` + filepath.Join(root, ".letsgo") + `"`
	if !strings.Contains(string(got), want) {
		t.Errorf("input %s does not contain %q", got, want)
	}
}

func TestApplyLayoutPluginSendsConfigDir(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	t.Setenv("LETSGO_FAKE_INPUT", input)

	digest := fakeHookPlugin(t, "letsgo-multi",
		`cat > "$LETSGO_FAKE_INPUT"; echo '{"archives":[{"name":"tools","binaries":["alpha"]}]}'`)

	root := t.TempDir()
	p := &Plan{
		RootDir:  root,
		Commands: []discover.MainPackage{{RelPath: "./cmd/alpha", BinaryName: "alpha"}},
		Plugins: map[plugin.Hook]plugin.Plugin{
			plugin.HookArchiveLayout: {Hook: plugin.HookArchiveLayout, Command: "letsgo-multi", Digest: digest},
		},
	}
	p.applyLayoutPlugin(context.Background())
	if !p.OK() {
		t.Fatalf("checks = %+v", p.Checks)
	}

	got, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `"config_dir":"` + filepath.Join(root, ".letsgo") + `"`
	if !strings.Contains(string(got), want) {
		t.Errorf("input %s does not contain %q", got, want)
	}
}
