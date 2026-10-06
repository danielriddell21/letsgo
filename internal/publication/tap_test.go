package publication

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/forgerelease"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/manifest"
	plandiff "github.com/danielriddell21/letsgo/plan"
	"github.com/danielriddell21/letsgo/plugin"
)

// fakeTap is a tap that remembers which paths were written to it, without
// caring what was sent.
type fakeTap struct {
	writes []string
}

func (f *fakeTap) ReadFile(_ context.Context, _ github.Repo, _ string) (*github.File, error) {
	return nil, nil
}

func (f *fakeTap) WriteFile(_ context.Context, _ github.Repo, in github.FileInput) error {
	f.writes = append(f.writes, in.Path)
	return nil
}

// failingTap is a tap that cannot be read.
type failingTap struct{}

func (failingTap) ReadFile(context.Context, github.Repo, string) (*github.File, error) {
	return nil, errors.New("tap unreachable")
}

func (failingTap) WriteFile(context.Context, github.Repo, github.FileInput) error {
	return errors.New("tap unreachable")
}

// writeFailingTap is a tap that reads fine and refuses to write one path.
type writeFailingTap struct {
	fakeTap
	path string
}

func (w *writeFailingTap) WriteFile(ctx context.Context, repo github.Repo, in github.FileInput) error {
	if in.Path == w.path {
		return errors.New("tap refused the write")
	}
	return w.fakeTap.WriteFile(ctx, repo, in)
}

func releasePlan() *plan.Plan {
	return &plan.Plan{
		Version: "1.2.3",
		Tag:     "v1.2.3",
		Source:  plan.Source{Location: discover.Location{Repo: discover.Repo{Host: "github.com", Owner: "you", Name: "foo"}}},
		Config:  &config.Config{},
	}
}

// built is a release of 1.2.3 that produced these artifacts, with the manifest
// that records them: the formulas are made from the manifest, not the builds.
func built(artifacts ...build.Artifact) *release.Result {
	m := &manifest.Manifest{Version: "1.2.3", Tag: "v1.2.3"}
	for _, a := range artifacts {
		record := manifest.Artifact{Name: a.Archive, OS: a.OS, Arch: a.Arch, SHA256: a.ArchiveSHA256}
		for _, b := range a.Binaries {
			record.Binaries = append(record.Binaries, manifest.Binary{Name: b.Name})
		}
		m.Artifacts = append(m.Artifacts, record)
	}
	return &release.Result{Artifacts: artifacts, Manifest: m}
}

func artifact(archive, goos, goarch, sum string, binaries ...string) build.Artifact {
	built := make([]build.Binary, len(binaries))
	for i, b := range binaries {
		built[i] = build.Binary{Name: b}
	}
	return build.Artifact{
		Archive: archive, OS: goos, Arch: goarch, ArchiveSHA256: sum, Binaries: built,
	}
}

// A prerelease's formula would overwrite the stable formula that `brew
// install foo` relies on, so publishTap must write @next only.
func TestPublishTapSkipsAPrerelease(t *testing.T) {
	t.Parallel()
	p := releasePlan()
	p.Version, p.Tag = "1.3.0-rc.1", "v1.3.0-rc.1"
	p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}

	result := built(artifact("foo_1.3.0-rc.1_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
	result.Manifest.Version, result.Manifest.Tag = p.Version, p.Tag
	tap := &fakeTap{}

	if err := publishTap(context.Background(), io.Discard, Options{Plan: p, Result: result, Tap: tap, Repo: github.Repo{Owner: "you", Name: "foo"}}); err != nil {
		t.Fatal(err)
	}
	if len(tap.writes) != 1 || tap.writes[0] != "Formula/foo@next.rb" {
		t.Errorf("writes = %v, want [Formula/foo@next.rb]", tap.writes)
	}
}

// A stable release must still write its formula normally, plus @next: the
// prerelease guard must not over-suppress the tap.
func TestPublishTapWritesAFormulaForAStableRelease(t *testing.T) {
	t.Parallel()
	p := releasePlan()
	p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}

	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
	tap := &fakeTap{}

	if err := publishTap(context.Background(), io.Discard, Options{Plan: p, Result: result, Tap: tap, Repo: github.Repo{Owner: "you", Name: "foo"}}); err != nil {
		t.Fatal(err)
	}
	if len(tap.writes) != 2 || tap.writes[0] != "Formula/foo.rb" || tap.writes[1] != "Formula/foo@next.rb" {
		t.Errorf("writes = %v, want [Formula/foo.rb Formula/foo@next.rb]", tap.writes)
	}
}

// `release --draft` must hold the tap back exactly as `draft = true` in the
// config does: a formula pointing at a draft's assets resolves to a 404.
func TestDraftFlagHoldsTheTapBack(t *testing.T) {
	t.Parallel()
	p := releasePlan()
	p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}
	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
	tap := &fakeTap{}

	p.MarkDraft()

	if err := publishTap(context.Background(), io.Discard, Options{Plan: p, Result: result, Tap: tap, Repo: github.Repo{Owner: "you", Name: "foo"}}); err != nil {
		t.Fatal(err)
	}
	if len(tap.writes) != 0 {
		t.Errorf("a draft release wrote to the tap: %v", tap.writes)
	}
}

func TestDraftFlagHoldsTheImagesBack(t *testing.T) {
	t.Parallel()
	p := releasePlan()
	result := &release.Result{Images: []release.ImageBuild{{}}}

	p.MarkDraft()

	// Reaches the draft guard before any registry call; a push would fail here.
	if err := publishImages(context.Background(), io.Discard, Options{Plan: p, Result: result}); err != nil {
		t.Fatalf("a draft release tried to push its images: %v", err)
	}
}

func TestPublishTapNamesTheFileItCouldNotWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{"the formula", "Formula/foo.rb", "publishing formula foo"},
		{"the next formula", "Formula/foo@next.rb", "publishing formula foo@next"},
		{"a tap file", "Casks/foo.rb", "publishing Casks/foo.rb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := releasePlan()
			p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}
			result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
			result.TapFiles = []plugin.TapFile{{Path: "Casks/foo.rb", Content: "cask"}}
			tap := &writeFailingTap{path: tt.path}

			err := publishTap(t.Context(), io.Discard, Options{Plan: p, Result: result, Tap: tap, Repo: github.Repo{Owner: "you", Name: "foo"}})

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestFormulasGroupsByArchive(t *testing.T) {
	t.Parallel()
	// Two archives produce two formulas: a formula names one archive per
	// platform, so alpha and beta cannot share one.
	result := built(
		artifact("alpha_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "alpha"),
		artifact("beta_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "b1", "beta"),
		artifact("alpha_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "a2", "alpha"),
	)

	got := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 2 {
		t.Fatalf("got %d formulas, want 2", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Errorf("formulas = %s, %s", got[0].Name, got[1].Name)
	}
	if len(got[0].Platforms) != 2 {
		t.Errorf("alpha has %d platforms, want 2", len(got[0].Platforms))
	}

	// The URL has to name the release the archives were published under.
	if !strings.Contains(got[0].Platforms[0].URL, "/releases/download/v1.2.3/alpha_1.2.3_linux_amd64.tar.gz") {
		t.Errorf("url = %q", got[0].Platforms[0].URL)
	}
	if got[0].Platforms[0].SHA256 != "a1" {
		t.Errorf("digest = %q", got[0].Platforms[0].SHA256)
	}
}

// desc and license come from the repository rather than from config, and are
// omitted when it has none: inventing either is worse than leaving them out.
func TestFormulasTakeMetadataFromTheRepository(t *testing.T) {
	t.Parallel()
	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "", "foo"))

	without := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)
	if without[0].Description != "" || without[0].License != "" {
		t.Errorf("metadata was invented: %+v", without[0])
	}
	if without[0].Homepage != "https://github.com/you/foo" {
		t.Errorf("homepage = %q", without[0].Homepage)
	}

	with := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, &github.RepoInfo{
		Description: "a tool", License: "MIT", Homepage: "https://foo.example",
	})
	if with[0].Description != "a tool" || with[0].License != "MIT" {
		t.Errorf("metadata = %+v", with[0])
	}
	if with[0].Homepage != "https://foo.example" {
		t.Errorf("homepage = %q", with[0].Homepage)
	}
}

// An archive holding several tools is one formula that installs all of them,
// not one formula per tool fighting over the same file. Without this, a
// collection whose commands include a name Homebrew core already uses would
// write a formula that shadows it.
func TestFormulasInstallEveryBinaryInTheArchive(t *testing.T) {
	t.Parallel()
	result := built(
		artifact("toolshed_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "crabs", "duck", "fish"),
		artifact("toolshed_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "a2", "crabs", "duck", "fish"),
	)

	got := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 1 {
		t.Fatalf("got %d formulas, want 1", len(got))
	}
	if got[0].Name != "toolshed" {
		t.Errorf("name = %q, want the archive's", got[0].Name)
	}
	if strings.Join(got[0].Binaries, ",") != "crabs,duck,fish" {
		t.Errorf("binaries = %q", got[0].Binaries)
	}

	rendered, err := got[0].Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), `bin.install "crabs", "duck", "fish"`) {
		t.Errorf("install line missing from:\n%s", rendered)
	}
}

// A variant is a second product from the same source, and a windowed build
// usually belongs in a cask. Writing it a formula anyway would put a package
// in the tap that the repository never asked for.
func TestFormulasLeaveOutAVariant(t *testing.T) {
	t.Parallel()
	p := releasePlan()
	p.Groups = []plan.Group{
		{Name: "foo"},
		{Name: "foo-gui", Variant: "gui"},
	}

	result := built(
		artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"),
		artifact("foo_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "a2", "foo"),
		artifact("foo-gui_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "g1", "foo"),
	)

	result.Manifest.Artifacts[2].Variant = "gui"

	got := formulas(p, result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 1 {
		names := make([]string, len(got))
		for i, f := range got {
			names[i] = f.Name
		}
		t.Fatalf("got formulas %v, want only foo", names)
	}
	if got[0].Name != "foo" {
		t.Errorf("formula = %q, want foo", got[0].Name)
	}
	// The variant's darwin archive must not be folded into the release's
	// formula either: it is a different build of the same command.
	if len(got[0].Platforms) != 2 {
		t.Errorf("foo has %d platforms, want the release's two", len(got[0].Platforms))
	}
	for _, p := range got[0].Platforms {
		if strings.Contains(p.URL, "foo-gui") {
			t.Errorf("the variant's archive leaked into the formula: %q", p.URL)
		}
	}
}

// Caveats come from the config rather than the repository, because they say
// what the program needs of the machine — which no API knows.
func TestFormulasCarryTheConfiguredCaveats(t *testing.T) {
	t.Parallel()
	p := releasePlan()
	p.Config.BrewCaveats = "needs a display"

	got := formulas(p, built(
		artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"),
	), github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 1 {
		t.Fatalf("got %d formulas, want 1", len(got))
	}
	if got[0].Caveats != "needs a display" {
		t.Errorf("Caveats = %q", got[0].Caveats)
	}
}

func observeTap(t *testing.T, existing map[string][]byte, writes map[string][]byte) []plandiff.Action {
	t.Helper()
	recorder := forgerelease.NewRecorder(nil)
	recorder.Files = existing
	tap := NewTapObserver(recorder)
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	for path, content := range writes {
		file, err := tap.ReadFile(t.Context(), repo, path)
		if err != nil {
			t.Fatal(err)
		}
		if file != nil && string(file.Content) == string(content) {
			continue
		}
		if err := tap.WriteFile(t.Context(), repo, github.FileInput{Path: path, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	return tap.Actions()
}

func TestTapObserverReportsAddChangeAndKeep(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		existing map[string][]byte
		want     plandiff.Op
	}{
		{"absent", nil, plandiff.Add},
		{"different", map[string][]byte{"Formula/tool.rb": []byte("old")}, plandiff.Change},
		{"identical", map[string][]byte{"Formula/tool.rb": []byte("new")}, plandiff.Keep},
	} {
		t.Run(tt.name, func(t *testing.T) {
			actions := observeTap(t, tt.existing, map[string][]byte{"Formula/tool.rb": []byte("new")})
			if len(actions) != 1 || actions[0].Op != tt.want || actions[0].Kind != plandiff.KindTap {
				t.Fatalf("actions = %+v, want one %q", actions, tt.want)
			}
		})
	}
}

func TestTapObserverNamesTheFileItCouldNotRead(t *testing.T) {
	t.Parallel()

	_, err := NewTapObserver(failingTap{}).ReadFile(t.Context(), github.Repo{}, "Formula/foo.rb")

	if err == nil || !strings.Contains(err.Error(), "reading Formula/foo.rb from the tap") {
		t.Errorf("err = %v, want it to name the file", err)
	}
}

func TestTapObserverRefusesAWriteWithoutARead(t *testing.T) {
	t.Parallel()
	tap := NewTapObserver(forgerelease.NewRecorder(nil))
	err := tap.WriteFile(t.Context(), github.Repo{}, github.FileInput{Path: "x"})
	if err == nil || !strings.Contains(err.Error(), "without being read") {
		t.Errorf("err = %v", err)
	}
}

func TestBlobFingerprintMatchesGit(t *testing.T) {
	t.Parallel()
	// `printf 'hello\n' | git hash-object --stdin`
	want := "blob:ce013625030ba8dba906f756967f9e9ca394464a"
	if got := BlobFingerprint([]byte("hello\n")); got != want {
		t.Errorf("BlobFingerprint = %q, want %q", got, want)
	}
}
