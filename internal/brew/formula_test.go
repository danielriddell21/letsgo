package brew_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
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
