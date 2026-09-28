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
	"github.com/danielriddell21/letsgo/internal/manifest"
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

// pinnedPlan is basePlan with a tap-files plugin pinned to a fake script that
// runs body.
func pinnedPlan(t *testing.T, command, body string) *plan.Plan {
	t.Helper()
	digest := fakePlugin(t, command, body)
	p := basePlan(t)
	p.Plugins = map[plugin.Hook]plugin.Plugin{
		plugin.HookTapFiles: {Hook: plugin.HookTapFiles, Command: command, Digest: digest},
	}
	return p
}

func TestApplyTapFilesPluginNoOpWhenUnpinned(t *testing.T) {
	p := basePlan(t)
	p.Plugins = map[plugin.Hook]plugin.Plugin{}

	files, err := applyTapFilesPlugin(context.Background(), p, nil, nil)
	if err != nil || files != nil {
		t.Errorf("applyTapFilesPlugin = %v, %v, want nil, nil", files, err)
	}
}

// Every way a tap-files plugin's answer, or its situation, can be wrong: no
// tap to write into, a path outside its lane, the same path twice, or the
// plugin simply failing. All of it stops the release before anything is
// built further.
func TestApplyTapFilesPluginFails(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		noTap            bool
	}{
		{
			"without a tap", `cat > /dev/null; echo '{"files":[]}'`,
			"no Homebrew tap is configured", true,
		},
		{
			"a path that escapes the tap",
			`cat > /dev/null; echo '{"files":[{"path":"../Formula/x.rb","content":"x"}]}'`,
			"not a valid tap path", false,
		},
		{
			"a path outside Casks",
			`cat > /dev/null; echo '{"files":[{"path":"Formula/x.rb","content":"x"}]}'`,
			"not a valid tap path", false,
		},
		{
			"an absolute path",
			`cat > /dev/null; echo '{"files":[{"path":"/Casks/x.rb","content":"x"}]}'`,
			"not a valid tap path", false,
		},
		{
			"a duplicate path", `cat > /dev/null; echo '{"files":[` +
				`{"path":"Casks/x.rb","content":"a"},{"path":"Casks/x.rb","content":"b"}]}'`,
			"more than once", false,
		},
		{
			"the plugin's own error", `echo "no macOS build to cask" >&2; exit 1`,
			"no macOS build to cask", false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := pinnedPlan(t, "letsgo-cask", tc.body)
			if tc.noTap {
				p.Tap = github.Repo{}
			}

			if _, err := applyTapFilesPlugin(context.Background(), p, nil, nil); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestApplyTapFilesPluginRendersFromTheArtifacts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LETSGO_FAKE_INPUT", filepath.Join(dir, "input.json"))

	p := pinnedPlan(t, "letsgo-cask", `
cat > "$LETSGO_FAKE_INPUT"
echo '{"files":[{"path":"Casks/gambit-gui.rb","content":"cask \"gambit-gui\""}]}'`)
	artifacts := []build.Artifact{
		{
			Archive: "gambit-gui_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64",
			ArchiveSHA256: strings.Repeat("a", 64), Binaries: []build.Binary{{Name: "gambit"}},
		},
	}

	files, err := applyTapFilesPlugin(context.Background(), p, artifacts, &github.RepoInfo{
		Description: "a gambit", License: "MIT", Homepage: "https://gambit.example",
	})
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
		// Description, licence and homepage come from the repository, not a
		// flag: IP-11's whole point.
		`"description":"a gambit"`, `"license":"MIT"`, `"homepage":"https://gambit.example"`,
		`"archive":"gambit-gui_1.2.0_darwin_arm64.tar.gz"`,
		`"sha256":"` + strings.Repeat("a", 64) + `"`,
		`"url":"https://github.com/you/gambit/releases/download/v1.2.0/gambit-gui_1.2.0_darwin_arm64.tar.gz"`,
		`"binaries":["gambit"]`,
		`"config_dir":"` + filepath.Join(p.RootDir, ".letsgo") + `"`,
	} {
		if !strings.Contains(string(input), want) {
			t.Errorf("input %s does not contain %q", input, want)
		}
	}
}

// Yank rebuilds the same input a release built, but from the previous
// release's manifest rather than fresh artifacts.
func TestTapFilesInputFromManifestRebuildsFromThePreviousRelease(t *testing.T) {
	m := &manifest.Manifest{
		Project: "gambit", Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{
				Name: "gambit-gui_1.2.0_darwin_arm64.tar.gz", Variant: "gui",
				OS: "darwin", Arch: "arm64", SHA256: strings.Repeat("a", 64), Binary: "gambit-gui",
			},
		},
	}
	repo := github.Repo{Owner: "you", Name: "gambit"}
	tap := github.Repo{Owner: "you", Name: "homebrew-tap"}

	in := TapFilesInputFromManifest(m, repo, tap, "a caveat")

	if in.Project != "gambit" || in.Version != "1.2.0" || in.Tag != "v1.2.0" ||
		in.Repo != "you/gambit" || in.Tap != "you/homebrew-tap" || in.Caveats != "a caveat" {
		t.Errorf("in = %+v", in)
	}
	if len(in.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", in.Artifacts)
	}

	a := in.Artifacts[0]
	if a.Archive != m.Artifacts[0].Name || a.Variant != "gui" || a.SHA256 != strings.Repeat("a", 64) ||
		a.URL != "https://github.com/you/gambit/releases/download/v1.2.0/"+m.Artifacts[0].Name ||
		len(a.Binaries) != 1 || a.Binaries[0] != "gambit-gui" {
		t.Errorf("artifact = %+v", a)
	}
}

// A release published before the manifest recorded a tag has none to read,
// so the same v-prefixed default a fresh release would use applies here too.
func TestTapFilesInputFromManifestDefaultsTheTagFromTheVersion(t *testing.T) {
	m := &manifest.Manifest{Project: "gambit", Version: "1.2.0"}

	in := TapFilesInputFromManifest(m, github.Repo{Owner: "you", Name: "gambit"}, github.Repo{}, "")
	if in.Tag != "v1.2.0" {
		t.Errorf("Tag = %q, want v1.2.0", in.Tag)
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
