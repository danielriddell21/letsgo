package lsp

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

func TestFormatEditsReturnsNilWhenAlreadyFormatted(t *testing.T) {
	text := "build linux/amd64\n"
	if edits := formatEdits("letsgo.mod", text); edits != nil {
		t.Errorf("edits = %+v, want nil", edits)
	}
}

func TestFormatEditsReturnsNilOnAParseError(t *testing.T) {
	if edits := formatEdits("letsgo.mod", "build (\n"); edits != nil {
		t.Errorf("edits = %+v, want nil", edits)
	}
}

// TestFormatEditsMatchesFileFormatByteForByte guards ED-5: the LSP edit and
// `letsgo fmt`'s own output must be identical, which holds by construction
// since formatEdits calls File.Format directly — this proves the wiring,
// not the formatter itself.
func TestFormatEditsMatchesFileFormatByteForByte(t *testing.T) {
	text := "build   linux/amd64\ntags    foo\n"
	edits := formatEdits("letsgo.mod", text)
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}

	f, err := config.Parse("letsgo.mod", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	want := string(f.Format())
	if edits[0].NewText != want {
		t.Errorf("NewText = %q, want %q", edits[0].NewText, want)
	}
	if want == text {
		t.Fatal("test fixture is already formatted, rewrite it to exercise a real edit")
	}
}

func TestFormatEditsRangeCoversTheWholeDocument(t *testing.T) {
	text := "build   linux/amd64\n"
	edits := formatEdits("letsgo.mod", text)
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	r := edits[0].Range
	if r.Start != (Position{0, 0}) {
		t.Errorf("Range.Start = %+v, want {0 0}", r.Start)
	}
	if r.End.Line != 1 {
		t.Errorf("Range.End.Line = %d, want 1", r.End.Line)
	}
}
