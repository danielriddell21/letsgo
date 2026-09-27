package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// fakePlugin writes a shell script that answers whatever body prints, and
// puts it on PATH under the given command name. A shell script is enough:
// the contract is a subprocess reading JSON and writing JSON.
func fakePlugin(t *testing.T, command, body string) (digest string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, command)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Prepended, not replaced: a script that inspects its real input (rather
	// than discarding it) needs the ordinary coreutils on PATH too.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func basePlan(t *testing.T) *plan.Plan {
	t.Helper()
	return &plan.Plan{
		Project: "gambit",
		Version: "1.2.0",
		Tag:     "v1.2.0",
		RootDir: t.TempDir(),
		HasRepo: true,
		Repo:    discover.Repo{Host: "github.com", Owner: "you", Name: "gambit"},
		Tap:     github.Repo{Owner: "you", Name: "homebrew-tap"},
		Config:  &config.Config{BrewCaveats: "a caveat"},
	}
}

func TestApplyTapFilesPluginNoOpWhenUnpinned(t *testing.T) {
	p := basePlan(t)
	p.Plugins = map[plugin.Hook]plugin.Plugin{}

	files, err := applyTapFilesPlugin(context.Background(), p, nil)
	if err != nil || files != nil {
		t.Errorf("applyTapFilesPlugin = %v, %v, want nil, nil", files, err)
	}
}

func TestApplyTapFilesPluginFailsWithoutATap(t *testing.T) {
	digest := fakePlugin(t, "letsgo-cask", `cat > /dev/null; echo '{"files":[]}'`)
	p := basePlan(t)
	p.Tap = github.Repo{}
	p.Plugins = map[plugin.Hook]plugin.Plugin{
		plugin.HookTapFiles: {Hook: plugin.HookTapFiles, Command: "letsgo-cask", Digest: digest},
	}

	if _, err := applyTapFilesPlugin(context.Background(), p, nil); err == nil ||
		!strings.Contains(err.Error(), "no Homebrew tap is configured") {
		t.Errorf("err = %v", err)
	}
}

func TestApplyTapFilesPluginRendersFromTheArtifacts(t *testing.T) {
	digest := fakePlugin(t, "letsgo-cask", `
cat > "$LETSGO_FAKE_INPUT"
echo '{"files":[{"path":"Casks/gambit-gui.rb","content":"cask \"gambit-gui\""}]}'`)
	dir := t.TempDir()
	t.Setenv("LETSGO_FAKE_INPUT", filepath.Join(dir, "input.json"))

	p := basePlan(t)
	p.Plugins = map[plugin.Hook]plugin.Plugin{
		plugin.HookTapFiles: {Hook: plugin.HookTapFiles, Command: "letsgo-cask", Digest: digest},
	}
	artifacts := []build.Artifact{
		{
			Archive: "gambit-gui_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64",
			ArchiveSHA256: strings.Repeat("a", 64), Binaries: []build.Binary{{Name: "gambit"}},
		},
	}

	files, err := applyTapFilesPlugin(context.Background(), p, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "Casks/gambit-gui.rb" {
		t.Fatalf("files = %+v", files)
	}

	input, err := os.ReadFile(filepath.Join(dir, "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"project":"gambit"`, `"version":"1.2.0"`, `"tag":"v1.2.0"`,
		`"repo":"you/gambit"`, `"tap":"you/homebrew-tap"`, `"caveats":"a caveat"`,
		`"archive":"gambit-gui_1.2.0_darwin_arm64.tar.gz"`,
		`"sha256":"` + strings.Repeat("a", 64) + `"`,
		`"url":"https://github.com/you/gambit/releases/download/v1.2.0/gambit-gui_1.2.0_darwin_arm64.tar.gz"`,
		`"binaries":["gambit"]`,
	} {
		if !strings.Contains(string(input), want) {
			t.Errorf("input %s does not contain %q", input, want)
		}
	}
}

func TestApplyTapFilesPluginFailsOnAnInvalidPath(t *testing.T) {
	for _, tc := range []struct {
		name, path string
	}{
		{"escapes the tap", "../Formula/x.rb"},
		{"outside Casks", "Formula/x.rb"},
		{"absolute", "/Casks/x.rb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digest := fakePlugin(t, "letsgo-cask",
				`cat > /dev/null; echo '{"files":[{"path":"`+tc.path+`","content":"x"}]}'`)
			p := basePlan(t)
			p.Plugins = map[plugin.Hook]plugin.Plugin{
				plugin.HookTapFiles: {Hook: plugin.HookTapFiles, Command: "letsgo-cask", Digest: digest},
			}

			if _, err := applyTapFilesPlugin(context.Background(), p, nil); err == nil ||
				!strings.Contains(err.Error(), "not a valid tap path") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestApplyTapFilesPluginFailsOnADuplicatePath(t *testing.T) {
	digest := fakePlugin(t, "letsgo-cask", `cat > /dev/null; echo '{"files":[`+
		`{"path":"Casks/x.rb","content":"a"},{"path":"Casks/x.rb","content":"b"}]}'`)
	p := basePlan(t)
	p.Plugins = map[plugin.Hook]plugin.Plugin{
		plugin.HookTapFiles: {Hook: plugin.HookTapFiles, Command: "letsgo-cask", Digest: digest},
	}

	if _, err := applyTapFilesPlugin(context.Background(), p, nil); err == nil ||
		!strings.Contains(err.Error(), "more than once") {
		t.Errorf("err = %v", err)
	}
}

func TestApplyTapFilesPluginFailsOnThePluginsOwnError(t *testing.T) {
	digest := fakePlugin(t, "letsgo-cask", `echo "no macOS build to cask" >&2; exit 1`)
	p := basePlan(t)
	p.Plugins = map[plugin.Hook]plugin.Plugin{
		plugin.HookTapFiles: {Hook: plugin.HookTapFiles, Command: "letsgo-cask", Digest: digest},
	}

	if _, err := applyTapFilesPlugin(context.Background(), p, nil); err == nil ||
		!strings.Contains(err.Error(), "no macOS build to cask") {
		t.Errorf("err = %v", err)
	}
}

func TestTapFileRecordsDigestTheContent(t *testing.T) {
	sum := sha256.Sum256([]byte("cask\n"))
	got := tapFileRecords([]plugin.TapFile{{Path: "Casks/x.rb", Content: "cask\n"}})
	if len(got) != 1 || got[0].Path != "Casks/x.rb" || got[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("tapFileRecords = %+v", got)
	}
	if got := tapFileRecords(nil); got != nil {
		t.Errorf("tapFileRecords(nil) = %+v, want nil", got)
	}
}
