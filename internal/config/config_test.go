package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/modsyntax"
)

const sample = `// letsgo.mod for foo

project foo

build (
	linux/amd64
	linux/arm64
	darwin/arm64
)

ldflags -s -w

archive (
	README.md
	LICENSE
)

budget linux/amd64 15MB

release prerelease=auto draft=false latest=auto

brew danielriddell21/homebrew-tap
`

func parse(t *testing.T, src string) *modsyntax.File {
	t.Helper()
	f, err := modsyntax.Parse("letsgo.mod", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func decode(t *testing.T, src string) *Config {
	t.Helper()
	cfg, err := Decode(parse(t, src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return cfg
}

func TestDecodeSample(t *testing.T) {
	cfg := decode(t, sample)

	if cfg.Project != "foo" {
		t.Errorf("Project = %q", cfg.Project)
	}
	want := []string{"linux/amd64", "linux/arm64", "darwin/arm64"}
	if strings.Join(cfg.Targets, ",") != strings.Join(want, ",") {
		t.Errorf("Targets = %v, want %v", cfg.Targets, want)
	}
	if strings.Join(cfg.LDFlags, " ") != "-s -w" {
		t.Errorf("LDFlags = %v", cfg.LDFlags)
	}
	if strings.Join(cfg.ArchiveFiles, ",") != "README.md,LICENSE" {
		t.Errorf("ArchiveFiles = %v", cfg.ArchiveFiles)
	}
	if cfg.Budgets["linux/amd64"] != "15MB" {
		t.Errorf("Budgets = %v", cfg.Budgets)
	}
	if pos := cfg.BudgetPos["linux/amd64"]; pos.Line != 18 || pos.Col != 1 {
		t.Errorf("BudgetPos = %+v, want {18 1}", pos)
	}
	if cfg.Prerelease != "auto" || cfg.Draft || cfg.Latest != "auto" {
		t.Errorf("Prerelease = %q, Draft = %v, Latest = %q", cfg.Prerelease, cfg.Draft, cfg.Latest)
	}
	if cfg.BrewTap != "danielriddell21/homebrew-tap" {
		t.Errorf("BrewTap = %q", cfg.BrewTap)
	}
}

// A single-argument directive and a block of the same keyword must mean the
// same thing, exactly as require does in go.mod.
func TestBlockAndLineFormsAgree(t *testing.T) {
	block := decode(t, "build (\n\tlinux/amd64\n\tdarwin/arm64\n)\n")
	lines := decode(t, "build linux/amd64\nbuild darwin/arm64\n")
	inline := decode(t, "build linux/amd64 darwin/arm64\n")

	want := "linux/amd64,darwin/arm64"
	for name, cfg := range map[string]*Config{"block": block, "lines": lines, "inline": inline} {
		if got := strings.Join(cfg.Targets, ","); got != want {
			t.Errorf("%s form gave %q, want %q", name, got, want)
		}
	}
}

func TestQuotedArguments(t *testing.T) {
	cfg := decode(t, `archive "release notes.md"`+"\n")
	if len(cfg.ArchiveFiles) != 1 || cfg.ArchiveFiles[0] != "release notes.md" {
		t.Fatalf("ArchiveFiles = %v", cfg.ArchiveFiles)
	}

	// A value needing quotes must come back quoted, or the round trip loses it.
	formatted := string(parse(t, `archive "release notes.md"`+"\n").Format())
	if !strings.Contains(formatted, `"release notes.md"`) {
		t.Errorf("quoting lost in formatting: %q", formatted)
	}
}

func TestUnknownDirectiveIsRejected(t *testing.T) {
	_, err := Decode(parse(t, "buidl linux/amd64\n"))
	if err == nil {
		t.Fatal("Decode accepted an unknown directive, want an error")
	}
	// Strictness is only useful if it points at the fix.
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("error offers no guidance: %v", err)
	}
}

func TestRejectsAmbiguousRepetition(t *testing.T) {
	// Two project names would mean one silently wins.
	if _, err := Decode(parse(t, "project foo\nproject bar\n")); err == nil {
		t.Error("Decode accepted a repeated project directive, want an error")
	}
	// Repeating a list directive is not ambiguous and must stay legal.
	if _, err := Decode(parse(t, "build linux/amd64\nbuild darwin/arm64\n")); err != nil {
		t.Errorf("Decode rejected a repeated list directive: %v", err)
	}
}

func TestRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"arity":            "project\n",
		"too many args":    "project foo bar\n",
		"budget arity":     "budget linux/amd64\n",
		"brew form":        "brew notataprepo\n",
		"release form":     "release draft\n",
		"release option":   "release nosuchoption=true\n",
		"prerelease value": "release prerelease=maybe\n",
		"draft value":      "release draft=yes\n",
		"latest value":     "release latest=maybe\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(parse(t, src)); err == nil {
				t.Errorf("Decode(%q) succeeded, want an error", src)
			}
		})
	}
}

func TestEmptyFile(t *testing.T) {
	cfg := decode(t, "")
	if cfg.Project != "" || len(cfg.Targets) != 0 {
		t.Errorf("empty file produced %+v", cfg)
	}
	if out := parse(t, "// just a comment\n").Format(); !strings.Contains(string(out), "just a comment") {
		t.Errorf("comment-only file lost its comment: %q", out)
	}
}

// Windows checkouts and editors both produce CRLF.
func TestCarriageReturnsAreHandled(t *testing.T) {
	cfg := decode(t, "project foo\r\nbuild linux/amd64\r\n")
	if cfg.Project != "foo" || len(cfg.Targets) != 1 {
		t.Errorf("CRLF input produced %+v", cfg)
	}
}

func TestUnknownNamesCarryTheSuggestion(t *testing.T) {
	for _, src := range []string{"buidl x\n", "disable chnagelog\n"} {
		_, err := Decode(parse(t, src))
		var se *modsyntax.SyntaxError
		if !errors.As(err, &se) || se.Wrong == "" || se.Suggest == "" {
			t.Errorf("%q: err = %v, want a suggestion", src, err)
		}
	}
	_, err := Decode(parse(t, "zzzzzzzz x\n"))
	var se *modsyntax.SyntaxError
	if !errors.As(err, &se) || se.Suggest != "" {
		t.Errorf("err = %v, want no suggestion", err)
	}
}
