package yank_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	"github.com/danielriddell21/letsgo/manifest"
)

// fakeForge is a release API that remembers what it was asked to change.
type fakeForge struct {
	release *github.Release
	updated github.ReleaseInput
}

func (f *fakeForge) ReleaseByTag(context.Context, github.Repo, string) (*github.Release, error) {
	return f.release, nil
}

func (f *fakeForge) UpdateRelease(_ context.Context, _ github.Repo, _ int64, in github.ReleaseInput) (*github.Release, error) {
	f.updated = in
	return &github.Release{TagName: in.TagName, Body: in.Body, Prerelease: in.Prerelease}, nil
}

func TestRunMarksTheReleaseAndEditsGoMod(t *testing.T) {
	dir := t.TempDir()
	goMod := dir + "/go.mod"
	if err := writeFile(goMod, "module example.com/foo\n\ngo 1.27\n"); err != nil {
		t.Fatal(err)
	}

	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.2.3", Body: "### Features\n\n- a thing\n"}}

	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge,
		Repo:   github.Repo{Owner: "you", Name: "foo"},
		Tag:    "v1.2.3",
		Reason: "ships a binary that reports the wrong version",
		GoMod:  goMod,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Prerelease rather than deleted: deleting breaks every checksum anyone
	// recorded, and the proxy has the module regardless.
	if !forge.updated.Prerelease {
		t.Error("the release was not marked as a prerelease")
	}
	if !strings.HasPrefix(forge.updated.Body, "> [!CAUTION]") {
		t.Errorf("no notice at the top of the description:\n%s", forge.updated.Body)
	}
	// The original notes have to survive: they are what the release was.
	if !strings.Contains(forge.updated.Body, "- a thing") {
		t.Errorf("the existing description was discarded:\n%s", forge.updated.Body)
	}
	if !strings.Contains(forge.updated.Body, "v1.2.4") {
		t.Errorf("the notice does not say what to use instead:\n%s", forge.updated.Body)
	}

	if !result.Retracted {
		t.Error("go.mod was not edited")
	}
	got := readFile(t, goMod)
	if !strings.Contains(got, "retract (\n\tv1.2.3 // ships a binary that reports the wrong version\n)") {
		t.Errorf("go.mod:\n%s", got)
	}
	if result.Next != "v1.2.4" {
		t.Errorf("Next = %q, want v1.2.4", result.Next)
	}
}

// A scoped module's go.mod retract directive names a bare version: the file
// already lives in the scope its tag prefix names, so the directive must not
// repeat it, and Retract would reject a prefixed tag as not a version anyway.
func TestRunStripsTheScopePrefixBeforeEditingGoMod(t *testing.T) {
	dir := t.TempDir()
	goMod := dir + "/go.mod"
	if err := writeFile(goMod, "module example.com/foo/services/api\n"); err != nil {
		t.Fatal(err)
	}

	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "services/api/v1.2.3", Body: "notes"}}

	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "services/api/v1.2.3", Prefix: "services/api/", Reason: "bad", GoMod: goMod,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Retracted {
		t.Fatal("go.mod was not edited")
	}
	got := readFile(t, goMod)
	if !strings.Contains(got, "retract (\n\tv1.2.3 // bad\n)") {
		t.Errorf("go.mod:\n%s", got)
	}
	if result.Next != "services/api/v1.2.4" {
		t.Errorf("Next = %q, want services/api/v1.2.4", result.Next)
	}
}

// Running yank twice must not stack notices or directives.
func TestRunIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	goMod := dir + "/go.mod"
	if err := writeFile(goMod, "module example.com/foo\n"); err != nil {
		t.Fatal(err)
	}

	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}}
	options := yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.2.3", Reason: "bad", GoMod: goMod,
	}

	if _, err := yank.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, goMod)

	forge.release.Body = forge.updated.Body
	second, err := yank.Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}

	if second.Retracted {
		t.Error("the second run reported a go.mod change")
	}
	if readFile(t, goMod) != first {
		t.Error("go.mod changed on the second run")
	}
	if strings.Count(forge.updated.Body, "[!CAUTION]") != 1 {
		t.Errorf("the notice was stacked:\n%s", forge.updated.Body)
	}
}

func TestRunRefusesAnUnknownRelease(t *testing.T) {
	forge := &fakeForge{release: nil}
	_, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"}, Tag: "v9.9.9",
	})
	if err == nil {
		t.Fatal("want an error for a tag with no release")
	}
}

func TestPreviousOf(t *testing.T) {
	tags := []string{"v1.0.0", "v1.1.0", "v1.2.0-rc.1", "v1.2.0", "v1.3.0", "not-a-version"}

	if got := yank.PreviousOf(tags, "v1.3.0", ""); got != "v1.2.0" {
		t.Errorf("PreviousOf = %q, want v1.2.0", got)
	}
	// A prerelease is not what a retracted release should send people to.
	if got := yank.PreviousOf(tags, "v1.2.0", ""); got != "v1.1.0" {
		t.Errorf("PreviousOf = %q, want v1.1.0", got)
	}
	// The first release has nothing to fall back to.
	if got := yank.PreviousOf(tags, "v1.0.0", ""); got != "" {
		t.Errorf("PreviousOf = %q, want empty", got)
	}
}

// A nested module's tags parse as valid versions too, so an unscoped search
// would sometimes hand a scoped retraction someone else's previous release.
func TestPreviousOfIgnoresAnotherScopesTags(t *testing.T) {
	tags := []string{
		"v9.0.0", // the root's own, newer tag — a different scope
		"services/api/v1.0.0", "services/api/v1.1.0",
	}

	got := yank.PreviousOf(tags, "services/api/v1.1.0", "services/api/")
	if got != "services/api/v1.0.0" {
		t.Errorf("PreviousOf = %q, want services/api/v1.0.0", got)
	}

	// A tag outside this scope is not a version in it at all.
	if got := yank.PreviousOf(tags, "v9.0.0", "services/api/"); got != "" {
		t.Errorf("PreviousOf = %q, want empty for a tag outside the scope", got)
	}
}

// A tag can carry the scope's prefix and still not parse as a version
// (MatchesTag only checks for a leading digit, not a full semver).
func TestPreviousOfRejectsATargetThatDoesNotParse(t *testing.T) {
	if got := yank.PreviousOf([]string{"v1.0.0"}, "v1", ""); got != "" {
		t.Errorf("PreviousOf = %q, want empty for an unparseable target", got)
	}
}

func TestNextAfterKeepsTheScopePrefix(t *testing.T) {
	if got := yank.NextAfter("v1.2.3", ""); got != "v1.2.4" {
		t.Errorf("NextAfter = %q, want v1.2.4", got)
	}
	if got := yank.NextAfter("services/api/v1.2.3", "services/api/"); got != "services/api/v1.2.4" {
		t.Errorf("NextAfter = %q, want services/api/v1.2.4", got)
	}
	// A tag outside the given scope has no next version in it.
	if got := yank.NextAfter("v1.2.3", "services/api/"); got != "" {
		t.Errorf("NextAfter = %q, want empty", got)
	}
	// MatchesTag only checks for a leading digit, not a full semver.
	if got := yank.NextAfter("v1", ""); got != "" {
		t.Errorf("NextAfter = %q, want empty for an unparseable tag", got)
	}
}

// fakeTap is a Homebrew tap that remembers what was written to it.
type fakeTap struct {
	files    map[string][]byte
	writes   []github.FileInput
	writeErr error
}

func (f *fakeTap) ReadFile(_ context.Context, _ github.Repo, path string) (*github.File, error) {
	content, ok := f.files[path]
	if !ok {
		return nil, nil
	}
	return &github.File{Path: path, SHA: "blob", Content: content}, nil
}

func (f *fakeTap) WriteFile(_ context.Context, _ github.Repo, in github.FileInput) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, in)
	return nil
}

// The release wrote the repository's description, licence and homepage into
// the formula, so a rollback that rebuilt it without them would quietly strip
// them from the tap.
func TestRunRollsBackTheFormulaWithItsMetadata(t *testing.T) {
	tap := &fakeTap{}
	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "foo", SHA256: "aaa"},
		},
	}

	_, err := yank.Run(context.Background(), yank.Options{
		Client: &fakeForge{release: &github.Release{ID: 7, TagName: "v1.3.0", Body: "notes"}},
		Repo:   github.Repo{Owner: "you", Name: "foo"},
		Tag:    "v1.3.0", Reason: "bad build",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: tap,
		Previous: "v1.2.0", Project: "foo",
		RepoInfo:  &github.RepoInfo{Description: "a tool", License: "MIT", Homepage: "https://foo.example"},
		Manifests: func(context.Context, string) (*manifest.Manifest, error) { return m, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var formula string
	for _, w := range tap.writes {
		if w.Path == "Formula/foo.rb" {
			formula = string(w.Content)
		}
	}
	for _, want := range []string{`desc "a tool"`, `license "MIT"`, `homepage "https://foo.example"`} {
		if !strings.Contains(formula, want) {
			t.Errorf("the rolled-back formula is missing %s:\n%s", want, formula)
		}
	}
}

// tapFilesFixture writes a fake tap-files plugin that answers with body, and
// pins it. A shell script is enough: the contract is a subprocess reading
// JSON and writing JSON.
func tapFilesFixture(t *testing.T, body string) (plugin.Plugin, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "letsgo-cask")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)

	return plugin.Plugin{
		Hook: plugin.HookTapFiles, Command: "letsgo-cask",
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}, t.TempDir()
}

// A cask left pointing at a retracted release is a bug nobody sees: it still
// resolves, just to bytes that say not to use them. Yank has to roll it back
// exactly as it rolls back the formula.
func TestRunRollsBackTheCaskToo(t *testing.T) {
	tapFilesPlugin, root := tapFilesFixture(t, `
cat > /dev/null
echo '{"files":[{"path":"Casks/foo.rb","content":"cask \"foo\""}]}'`)

	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.3.0", Body: "notes"}}
	tap := &fakeTap{}

	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "foo", SHA256: "aaa"},
		},
	}

	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.3.0", Reason: "bad build",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: tap,
		Previous:       "v1.2.0",
		TapFilesPlugin: tapFilesPlugin, PluginRoot: root,
		Manifests: func(context.Context, string) (*manifest.Manifest, error) { return m, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	// The formula rolls back too, from the same manifest; the cask is what
	// this test is about.
	var cask *github.FileInput
	for i, w := range tap.writes {
		if w.Path == "Casks/foo.rb" {
			cask = &tap.writes[i]
		}
	}
	if cask == nil {
		t.Fatalf("no write to Casks/foo.rb, tap writes = %+v", tap.writes)
	}
	if !strings.Contains(string(cask.Content), `cask "foo"`) {
		t.Errorf("cask content = %s", cask.Content)
	}
	if len(result.TapFiles) != 1 || result.TapFiles[0] != "Casks/foo.rb" {
		t.Errorf("result.TapFiles = %v", result.TapFiles)
	}
}

// No tap-files plugin pinned means nothing to roll back, and no error either.
func TestRunSkipsTheCaskWhenNoPluginIsPinned(t *testing.T) {
	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.3.0", Body: "notes"}}
	tap := &fakeTap{}

	m := &manifest.Manifest{Version: "1.2.0", Tag: "v1.2.0"}

	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.3.0", Reason: "bad build",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: tap,
		Previous:  "v1.2.0",
		Manifests: func(context.Context, string) (*manifest.Manifest, error) { return m, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TapFiles) != 0 {
		t.Errorf("result.TapFiles = %v, want none", result.TapFiles)
	}
}

// A yanked release with no earlier one to fall back to leaves the tap alone
// — the same as a release that was the first.
func TestRunLeavesTheTapWhenThereIsNoEarlierRelease(t *testing.T) {
	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.0.0", Body: "notes"}}
	tap := &fakeTap{}

	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.0.0", Reason: "bad build",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: tap,
		Manifests: func(context.Context, string) (*manifest.Manifest, error) {
			t.Fatal("no earlier release means nothing to rebuild from")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tap.writes) != 0 || len(result.Formulas) != 0 || len(result.TapFiles) != 0 {
		t.Errorf("result = %+v, tap writes = %v, want no tap activity", result, tap.writes)
	}
}

// The plugin failing stops the rollback exactly as it stops a release: a cask
// nobody can account for is not one to publish.
func TestRunFailsWhenTheTapFilesPluginFails(t *testing.T) {
	tapFilesPlugin, root := tapFilesFixture(t, `echo "no macOS build to cask" >&2; exit 1`)

	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.3.0", Body: "notes"}}
	m := &manifest.Manifest{Version: "1.2.0", Tag: "v1.2.0"}

	_, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.3.0", Reason: "bad build",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: &fakeTap{},
		Previous:       "v1.2.0",
		TapFilesPlugin: tapFilesPlugin, PluginRoot: root,
		Manifests: func(context.Context, string) (*manifest.Manifest, error) { return m, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "no macOS build to cask") {
		t.Errorf("err = %v, want the plugin's own message", err)
	}
}

// A tap that refuses the write fails the retraction rather than reporting a
// cask rolled back that was not.
func TestRunFailsWhenTheTapRefusesTheCaskWrite(t *testing.T) {
	tapFilesPlugin, root := tapFilesFixture(t, `
cat > /dev/null
echo '{"files":[{"path":"Casks/foo.rb","content":"cask \"foo\""}]}'`)

	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.3.0", Body: "notes"}}
	m := &manifest.Manifest{Version: "1.2.0", Tag: "v1.2.0"}

	_, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.3.0", Reason: "bad build",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: &fakeTap{writeErr: errors.New("boom")},
		Previous:       "v1.2.0",
		TapFilesPlugin: tapFilesPlugin, PluginRoot: root,
		Manifests: func(context.Context, string) (*manifest.Manifest, error) { return m, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the tap's own refusal", err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// go.mod is read and written through the hooks when they are given, so that a
// plan can see the edit without making it.
func TestRunEditsGoModThroughTheHooks(t *testing.T) {
	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}}

	var written []byte
	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.2.3", Reason: "bad", GoMod: "never/on/disk/go.mod",
		ReadGoMod: func(path string) ([]byte, error) {
			if path != "never/on/disk/go.mod" {
				t.Errorf("read %s", path)
			}
			return []byte("module example.com/foo\n"), nil
		},
		WriteGoMod: func(_ string, data []byte) error { written = data; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Retracted || !strings.Contains(string(written), "retract") {
		t.Errorf("retracted = %v, written:\n%s", result.Retracted, written)
	}
}

func TestRunReportsAGoModThatCannotBeWritten(t *testing.T) {
	forge := &fakeForge{release: &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}}
	_, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"},
		Tag: "v1.2.3", GoMod: "go.mod",
		ReadGoMod:  func(string) ([]byte, error) { return []byte("module example.com/foo\n"), nil },
		WriteGoMod: func(string, []byte) error { return errors.New("read-only") },
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("err = %v, want the write failure", err)
	}
}

// A release already marked as retracted is not edited again: there is nothing
// to change, so nothing is written.
func TestRunLeavesAReleaseThatIsAlreadyRetracted(t *testing.T) {
	forge := &fakeForge{release: &github.Release{
		ID: 7, TagName: "v1.2.3", Prerelease: true, Body: "> [!CAUTION]\n> **This release is retracted.**\n\nnotes",
	}}

	result, err := yank.Run(context.Background(), yank.Options{
		Client: forge, Repo: github.Repo{Owner: "you", Name: "foo"}, Tag: "v1.2.3", Reason: "bad",
	})
	if err != nil {
		t.Fatal(err)
	}
	if forge.updated.TagName != "" {
		t.Errorf("the release was edited anyway: %+v", forge.updated)
	}
	if result.Release != forge.release {
		t.Errorf("result.Release = %+v", result.Release)
	}
}

// @next is rolled back with the formula, but only while it names the release
// being retracted.
func TestRunRollsBackNextOnlyWhileItNamesTheRetractedRelease(t *testing.T) {
	artifact := manifest.Artifact{Name: "foo_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "foo", SHA256: "aaa"}
	previous := &manifest.Manifest{Version: "1.2.0", Tag: "v1.2.0", Artifacts: []manifest.Artifact{artifact}}

	nextAt := func(version string) map[string][]byte {
		f := brew.FormulasFor(&manifest.Manifest{Version: version, Tag: "v" + version, Artifacts: []manifest.Artifact{artifact}},
			github.Repo{Owner: "you", Name: "foo"}, nil, "foo", "")[0]
		f.Name = brew.NextName(f.Name)
		content, err := f.Render()
		if err != nil {
			t.Fatal(err)
		}
		return map[string][]byte{f.FileName(): content}
	}

	for name, tc := range map[string]struct {
		next   string
		writes bool
	}{
		"names the yanked release": {"1.3.0", true},
		"has moved on":             {"1.4.0", false},
	} {
		t.Run(name, func(t *testing.T) {
			tap := &fakeTap{files: nextAt(tc.next)}
			_, err := yank.Run(context.Background(), yank.Options{
				Client: &fakeForge{release: &github.Release{ID: 7, TagName: "v1.3.0", Body: "notes"}},
				Repo:   github.Repo{Owner: "you", Name: "foo"},
				Tag:    "v1.3.0", Reason: "bad build",
				Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: tap,
				Previous: "v1.2.0", Project: "foo",
				Manifests: func(context.Context, string) (*manifest.Manifest, error) { return previous, nil },
			})
			if err != nil {
				t.Fatal(err)
			}

			var next *github.FileInput
			for i, w := range tap.writes {
				if strings.HasSuffix(w.Path, "@next.rb") {
					next = &tap.writes[i]
				}
			}
			if (next != nil) != tc.writes {
				t.Fatalf("@next written = %v, want %v (writes %+v)", next != nil, tc.writes, tap.writes)
			}
			if next != nil && !strings.Contains(string(next.Content), `"1.2.0"`) {
				t.Errorf("@next was not rolled back to 1.2.0:\n%s", next.Content)
			}
		})
	}
}
