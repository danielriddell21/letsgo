package yank_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
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

	if got := yank.PreviousOf(tags, "v1.3.0"); got != "v1.2.0" {
		t.Errorf("PreviousOf = %q, want v1.2.0", got)
	}
	// A prerelease is not what a retracted release should send people to.
	if got := yank.PreviousOf(tags, "v1.2.0"); got != "v1.1.0" {
		t.Errorf("PreviousOf = %q, want v1.1.0", got)
	}
	// The first release has nothing to fall back to.
	if got := yank.PreviousOf(tags, "v1.0.0"); got != "" {
		t.Errorf("PreviousOf = %q, want empty", got)
	}
}

func TestFormulasFromRebuildsFromTheManifest(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Binary: "foo", SHA256: "aaa"},
			{Name: "foo_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "foo", SHA256: "bbb"},
			{Name: "foo_1.2.0_windows_amd64.zip", OS: "windows", Arch: "amd64", Binary: "foo", SHA256: "ccc"},
		},
	}

	formulas := yank.FormulasFrom(m, github.Repo{Owner: "you", Name: "foo"}, "foo")
	if len(formulas) != 1 {
		t.Fatalf("got %d formulas", len(formulas))
	}

	rendered, err := formulas[0].Render()
	if err != nil {
		t.Fatal(err)
	}
	out := string(rendered)

	// The digests have to be the ones that release published, which are
	// recorded exactly once — in its own manifest.
	for _, want := range []string{`version "1.2.0"`, "aaa", "bbb", "releases/download/v1.2.0/"} {
		if !strings.Contains(out, want) {
			t.Errorf("formula is missing %q:\n%s", want, out)
		}
	}
}

// A retraction republishes the formulas a release published. A variant
// published none, so rebuilding one from the manifest would create a package
// the release never had — during a retraction, of all moments.
func TestFormulasFromSkipsAVariant(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "foo", SHA256: "aaa"},
			{
				Name: "foo-gui_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64",
				Binary: "foo", SHA256: "bbb", Variant: "gui",
			},
		},
	}

	formulas := yank.FormulasFrom(m, github.Repo{Owner: "you", Name: "foo"}, "foo")
	if len(formulas) != 1 {
		names := make([]string, len(formulas))
		for i, f := range formulas {
			names[i] = f.Name
		}
		t.Fatalf("got formulas %v, want only foo", names)
	}
	if formulas[0].Name != "foo" {
		t.Errorf("formula = %q, want foo", formulas[0].Name)
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
