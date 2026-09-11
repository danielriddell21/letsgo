package config

import (
	"strings"
	"testing"
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

release prerelease=auto draft=false

brew danielriddell21/homebrew-tap
`

func parse(t *testing.T, src string) *File {
	t.Helper()
	f, err := Parse("letsgo.mod", []byte(src))
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
	if cfg.Prerelease != "auto" || cfg.Draft {
		t.Errorf("Prerelease = %q, Draft = %v", cfg.Prerelease, cfg.Draft)
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

// The formatter must be a fixed point: formatting formatted output changes
// nothing, or letsgo fmt would fight itself.
func TestFormatIsIdempotent(t *testing.T) {
	once := parse(t, sample).Format()
	twice := parse(t, string(once)).Format()

	if string(once) != string(twice) {
		t.Errorf("formatting is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
	if string(once) != sample {
		t.Errorf("canonical input was reformatted:\n--- got ---\n%s\n--- want ---\n%s", once, sample)
	}
}

func TestFormatTidiesMess(t *testing.T) {
	messy := "\n\n\n   project    foo   \n\n\n\n   build(\n" +
		"       linux/amd64\n" +
		"  )\n\n\n"
	want := "project foo\n\nbuild (\n\tlinux/amd64\n)\n"

	got := string(parse(t, messy).Format())
	if got != want {
		t.Errorf("Format() =\n%q\nwant\n%q", got, want)
	}
}

func TestCommentsSurviveFormatting(t *testing.T) {
	src := "// why this project is named oddly\nproject foo // trailing note\n"
	got := string(parse(t, src).Format())
	if got != src {
		t.Errorf("Format() = %q, want %q", got, src)
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

func TestSyntaxErrorsCarryPositions(t *testing.T) {
	cases := map[string]string{
		"unclosed block":   "build (\n\tlinux/amd64\n",
		"stray close":      "project foo\n)\n",
		"nested block":     "build (\n\tarchive (\n\t)\n)\n",
		"unterminated str": "project \"foo\n",
		"junk after close": "build (\n\tlinux/amd64\n) extra\n",
		"anonymous block":  "(\n)\n",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("letsgo.mod", []byte(src))
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", src)
			}
			var syntaxErr *SyntaxError
			if !errorsAs(err, &syntaxErr) {
				t.Fatalf("error is %T, want *SyntaxError: %v", err, err)
			}
			if syntaxErr.Pos.Line < 1 {
				t.Errorf("error has no line number: %v", err)
			}
			if !strings.HasPrefix(err.Error(), "letsgo.mod:") {
				t.Errorf("error does not name the file: %v", err)
			}
		})
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

func errorsAs(err error, target **SyntaxError) bool {
	for err != nil {
		if e, ok := err.(*SyntaxError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
