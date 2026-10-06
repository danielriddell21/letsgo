package brew_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/manifest"
)

func sample() brew.Formula {
	return brew.Formula{
		Name: "my-tool", Binaries: []string{"my-tool"}, Version: "1.2.3",
		Homepage:    "https://github.com/you/my-tool",
		Description: `A tool that says "hello"`,
		License:     "MIT",
		Platforms: []brew.Platform{
			{OS: "linux", Arch: "arm64", URL: "https://e.test/l_arm64.tar.gz", SHA256: "1111"},
			{OS: "darwin", Arch: "amd64", URL: "https://e.test/d_amd64.tar.gz", SHA256: "2222"},
			{OS: "darwin", Arch: "arm64", URL: "https://e.test/d_arm64.tar.gz", SHA256: "3333"},
			{OS: "linux", Arch: "amd64", URL: "https://e.test/l_amd64.tar.gz", SHA256: "4444"},
			{OS: "windows", Arch: "amd64", URL: "https://e.test/w.zip", SHA256: "5555"},
		},
	}
}

func TestClassNameFollowsHomebrewConvention(t *testing.T) {
	for binary, want := range map[string]string{
		"letsgo":     "Letsgo",
		"my-tool":    "MyTool",
		"my_tool":    "MyTool",
		"foo.bar":    "FooBar",
		"go-version": "GoVersion",
		"foo@next":   "FooATNext",
	} {
		if got := (brew.Formula{Name: binary}).ClassName(); got != want {
			t.Errorf("ClassName(%q) = %q, want %q", binary, got, want)
		}
	}
}

func TestFileName(t *testing.T) {
	if got := sample().FileName(); got != "Formula/my-tool.rb" {
		t.Errorf("FileName = %q", got)
	}
}

func TestRenderCoversEveryHomebrewPlatform(t *testing.T) {
	out, err := sample().Render()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, want := range []string{
		"class MyTool < Formula",
		`desc "A tool that says \"hello\""`,
		`version "1.2.3"`,
		`license "MIT"`,
		"on_macos do", "on_linux do", "on_intel do", "on_arm do",
		`sha256 "1111"`, `sha256 "2222"`, `sha256 "3333"`, `sha256 "4444"`,
		`bin.install "my-tool"`,
		`assert_match "1.2.3", shell_output("#{bin}/my-tool --version")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formula is missing %q:\n%s", want, got)
		}
	}

	// Homebrew has no Windows, so a zip in the release must not reach the tap.
	if strings.Contains(got, "5555") || strings.Contains(got, ".zip") {
		t.Errorf("a Windows archive reached the formula:\n%s", got)
	}
}

// Identical input must render identical bytes, or publishing would commit to
// somebody else's repository on every release whether or not anything changed.
func TestRenderIsDeterministic(t *testing.T) {
	first, err := sample().Render()
	if err != nil {
		t.Fatal(err)
	}

	shuffled := sample()
	shuffled.Platforms[0], shuffled.Platforms[3] = shuffled.Platforms[3], shuffled.Platforms[0]
	second, err := shuffled.Render()
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Errorf("the same release rendered differently:\n%s\n---\n%s", first, second)
	}
}

func TestRenderOmitsOptionalFields(t *testing.T) {
	f := sample()
	f.Description, f.License = "", ""

	out, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// An invented description is worse than none; brew accepts a formula
	// without either field.
	if strings.Contains(got, "desc ") || strings.Contains(got, "license ") {
		t.Errorf("an absent field was rendered anyway:\n%s", got)
	}
}

func TestRenderRefusesAnUninstallableRelease(t *testing.T) {
	f := sample()
	f.Platforms = []brew.Platform{{OS: "windows", Arch: "amd64"}}
	if _, err := f.Render(); err == nil {
		t.Error("want an error for a release with nothing Homebrew can install")
	}

	if _, err := (brew.Formula{Name: "x", Binaries: []string{"x"}}).Render(); err == nil {
		t.Error("want an error for an incomplete formula")
	}
}

func TestParseTap(t *testing.T) {
	for in, want := range map[string]string{
		"you/homebrew-tap":   "you/homebrew-tap",
		"you/tap":            "you/homebrew-tap",
		"  you/brews  ":      "you/homebrew-brews",
		"org/homebrew-tools": "org/homebrew-tools",
	} {
		got, err := brew.ParseTap(in)
		if err != nil {
			t.Errorf("ParseTap(%q): %v", in, err)
			continue
		}
		if got.Owner+"/"+got.Name != want {
			t.Errorf("ParseTap(%q) = %s/%s, want %s", in, got.Owner, got.Name, want)
		}
	}

	for _, in := range []string{"", "you", "/tap", "you/", "you/tap/extra"} {
		if _, err := brew.ParseTap(in); err == nil {
			t.Errorf("ParseTap(%q) should have failed", in)
		}
	}
}

// A formula's caveats are the one part of it nothing else can supply: the
// release knows the digests and the repository knows its description, but only
// the author knows what the program needs of the machine it lands on.
func TestFormulaRendersCaveats(t *testing.T) {
	f := brew.Formula{
		Name: "vivarium", Binaries: []string{"vivarium"},
		Version: "1.0.0", Homepage: "https://github.com/you/vivarium",
		Caveats: "The native GUI is macOS-only: on macOS install the cask.\nElsewhere use `vivarium headless`.",
		Platforms: []brew.Platform{
			{OS: "darwin", Arch: "arm64", URL: "https://example.test/a.tar.gz", SHA256: "aaa"},
		},
	}

	out, err := f.Render()
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := string(out)

	// The squiggly heredoc strips the common indent, so every line has to
	// carry the same prefix or Ruby takes the wrong amount off.
	for _, want := range []string{
		"  def caveats\n    <<~EOS\n",
		"      The native GUI is macOS-only: on macOS install the cask.\n",
		"      Elsewhere use `vivarium headless`.\n",
		"    EOS\n  end\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formula is missing %q:\n%s", want, got)
		}
	}
}

// A formula without caveats has no caveats block at all, rather than an empty
// one Homebrew would print as a blank line.
func TestFormulaOmitsAnEmptyCaveats(t *testing.T) {
	f := brew.Formula{
		Name: "foo", Binaries: []string{"foo"},
		Version: "1.0.0", Homepage: "https://example.test",
		Platforms: []brew.Platform{
			{OS: "linux", Arch: "amd64", URL: "https://example.test/a.tar.gz", SHA256: "aaa"},
		},
	}
	out, err := f.Render()
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(string(out), "caveats") {
		t.Errorf("a formula with no caveats should have no caveats block:\n%s", out)
	}
}

func TestFormulasForRebuildsFromTheManifest(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Binary: "foo", SHA256: "aaa"},
			{Name: "foo_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "foo", SHA256: "bbb"},
			{Name: "foo_1.2.0_windows_amd64.zip", OS: "windows", Arch: "amd64", Binary: "foo", SHA256: "ccc"},
		},
	}

	formulas := brew.FormulasFor(m, github.Repo{Owner: "you", Name: "foo"}, nil, "foo", "")
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
func TestFormulasForSkipsAVariant(t *testing.T) {
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

	formulas := brew.FormulasFor(m, github.Repo{Owner: "you", Name: "foo"}, nil, "foo", "")
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

// A release writes the repository's description, licence and homepage into the
// formula, and a rollback goes through this same builder, so it cannot drop
// them: the two used to be separate builders, and the rollback's omitted them.
func TestFormulasForCarriesTheRepositoryMetadataAndCaveats(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Binary: "foo", SHA256: "aaa"},
		},
	}
	repo := github.Repo{Owner: "you", Name: "foo"}

	bare := brew.FormulasFor(m, repo, nil, "foo", "")[0]
	if bare.Description != "" || bare.License != "" || bare.Homepage != "https://github.com/you/foo" {
		t.Errorf("metadata was invented: %+v", bare)
	}

	full := brew.FormulasFor(m, repo, &github.RepoInfo{
		Description: "a tool", License: "MIT", Homepage: "https://foo.example",
	}, "foo", "needs a display")[0]
	if full.Description != "a tool" || full.License != "MIT" || full.Homepage != "https://foo.example" {
		t.Errorf("metadata = %+v", full)
	}
	if full.Caveats != "needs a display" {
		t.Errorf("Caveats = %q", full.Caveats)
	}

	// A repository with no homepage of its own keeps the repository's page.
	noPage := brew.FormulasFor(m, repo, &github.RepoInfo{Description: "a tool"}, "foo", "")[0]
	if noPage.Homepage != "https://github.com/you/foo" {
		t.Errorf("homepage = %q", noPage.Homepage)
	}
}

// An archive's name is its formula's, and an archive holding several tools is
// one formula installing all of them; one that predates the manifest recording
// a name falls back to the project's.
func TestFormulasForNamesAndGroupsByArchive(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{
				Name: "toolshed_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "a1",
				Binaries: []manifest.Binary{{Name: "crabs"}, {Name: "duck"}},
			},
			{
				Name: "toolshed_1.2.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", SHA256: "a2",
				Binaries: []manifest.Binary{{Name: "crabs"}, {Name: "duck"}},
			},
			{Name: "_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "b1"},
		},
	}

	got := brew.FormulasFor(m, github.Repo{Owner: "you", Name: "foo"}, nil, "legacy", "")

	if len(got) != 2 || got[0].Name != "legacy" || got[1].Name != "toolshed" {
		t.Fatalf("formulas = %+v, want legacy and toolshed", got)
	}
	if strings.Join(got[1].Binaries, ",") != "crabs,duck" || len(got[1].Platforms) != 2 {
		t.Errorf("toolshed = %+v", got[1])
	}
	if strings.Join(got[0].Binaries, ",") != "legacy" {
		t.Errorf("legacy binaries = %v, want the project's name", got[0].Binaries)
	}
}

// A manifest written before tags were recorded still has a download URL.
func TestFormulasForDerivesAMissingTag(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.0",
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Binary: "foo", SHA256: "aaa"},
		},
	}

	got := brew.FormulasFor(m, github.Repo{Owner: "you", Name: "foo"}, nil, "foo", "")

	if url := got[0].Platforms[0].URL; !strings.Contains(url, "/releases/download/v1.2.0/") {
		t.Errorf("url = %q", url)
	}
}
