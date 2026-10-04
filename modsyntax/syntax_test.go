package modsyntax

import (
	"errors"
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

release prerelease=auto draft=false latest=auto

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
			if !errors.As(err, &syntaxErr) {
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

func TestParseRecordsWordSpans(t *testing.T) {
	f, err := Parse("letsgo.mod", []byte("plugin archive \"my plugin\" v1\nbuild x (\n\tlinux/amd64  arm\n)\n"))
	if err != nil {
		t.Fatal(err)
	}
	line := f.Stmts[0].(*Line)
	if line.KeywordSpan != (Span{1, 7}) {
		t.Errorf("keyword span = %v, want {1 7}", line.KeywordSpan)
	}
	want := []Span{{8, 15}, {16, 27}, {28, 30}}
	if len(line.ArgSpans) != len(want) {
		t.Fatalf("arg spans = %v, want %v", line.ArgSpans, want)
	}
	for i, w := range want {
		if line.ArgSpans[i] != w {
			t.Errorf("arg span %d = %v, want %v", i, line.ArgSpans[i], w)
		}
	}

	block := f.Stmts[1].(*Block)
	if block.KeywordSpan != (Span{1, 6}) || len(block.ArgSpans) != 1 || block.ArgSpans[0] != (Span{7, 8}) || block.Close != (Position{4, 1}) {
		t.Errorf("block = %+v", block)
	}
	inner := block.Lines[0]
	if inner.ArgSpans[0] != (Span{2, 13}) || inner.ArgSpans[1] != (Span{15, 18}) {
		t.Errorf("inner spans = %v", inner.ArgSpans)
	}
}

func TestParseLenientKeepsWhatItCanRead(t *testing.T) {
	f, err := ParseLenient("letsgo.mod", []byte("project foo\n)\nplugin (\n\tarchive x v1\n"))
	var se *SyntaxError
	if !errors.As(err, &se) || se.Pos.Line != 2 {
		t.Fatalf("err = %v, want the stray ) on line 2", err)
	}
	if len(f.Stmts) != 2 {
		t.Fatalf("stmts = %d, want 2", len(f.Stmts))
	}
	block := f.Stmts[1].(*Block)
	if block.Close != (Position{}) || len(block.Lines) != 1 {
		t.Errorf("block = %+v, want open with its line", block)
	}
	if _, err := Parse("letsgo.mod", []byte("project foo\n)\n")); err == nil {
		t.Error("Parse accepted a stray )")
	}
}
