package yank_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/yank"
)

func retract(t *testing.T, in, version, reason string) string {
	t.Helper()
	out, changed, err := yank.Retract([]byte(in), version, reason)
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if !changed {
		t.Fatal("Retract reported no change")
	}
	return string(out)
}

func TestRetractAppendsABlockWhenThereIsNone(t *testing.T) {
	got := retract(t, "module example.com/foo\n\ngo 1.27\n", "v1.2.3", "ships a broken binary")

	want := "module example.com/foo\n\ngo 1.27\n\nretract (\n\tv1.2.3 // ships a broken binary\n)\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// The rationale is the only part of a retraction that explains anything: it is
// what `go list -m -retracted` shows to whoever is about to depend on it.
func TestRetractCarriesTheReason(t *testing.T) {
	got := retract(t, "module example.com/foo\n", "v1.2.3", "  wrong\n  version  embedded ")

	if !strings.Contains(got, "v1.2.3 // wrong version embedded") {
		t.Errorf("reason was not flattened onto the line:\n%s", got)
	}
}

func TestRetractWithoutAReason(t *testing.T) {
	got := retract(t, "module example.com/foo\n", "v1.2.3", "")

	if !strings.Contains(got, "\tv1.2.3\n") || strings.Contains(got, "//") {
		t.Errorf("got:\n%s", got)
	}
}

func TestRetractInsertsIntoAnExistingBlockInOrder(t *testing.T) {
	in := "module example.com/foo\n\nretract (\n\tv1.0.0 // old\n\tv2.0.0 // newer\n)\n"

	got := retract(t, in, "v1.5.0", "middle")

	want := "module example.com/foo\n\nretract (\n\tv1.0.0 // old\n\tv1.5.0 // middle\n\tv2.0.0 // newer\n)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRetractExtendsSingleLineForm(t *testing.T) {
	in := "module example.com/foo\n\nretract v1.0.0 // old\n\ngo 1.27\n"

	got := retract(t, in, "v1.1.0", "newer")

	want := "module example.com/foo\n\nretract v1.0.0 // old\nretract v1.1.0 // newer\n\ngo 1.27\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Running yank twice must not retract the same version twice.
func TestRetractIsIdempotent(t *testing.T) {
	in := "module example.com/foo\n\nretract (\n\tv1.2.3 // bad\n)\n"

	out, changed, err := yank.Retract([]byte(in), "v1.2.3", "bad again")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a second retraction of the same version reported a change")
	}
	if string(out) != in {
		t.Errorf("the file was rewritten:\n%s", out)
	}
}

// A retracted range is not something this understands well enough to reorder
// around, and must not be mangled.
func TestRetractLeavesRangesAlone(t *testing.T) {
	in := "module example.com/foo\n\nretract (\n\t[v1.0.0, v1.1.0] // a bad run\n)\n"

	got := retract(t, in, "v2.0.0", "also bad")

	if !strings.Contains(got, "[v1.0.0, v1.1.0] // a bad run") {
		t.Errorf("the range was damaged:\n%s", got)
	}
	if !strings.Contains(got, "v2.0.0 // also bad") {
		t.Errorf("the new entry is missing:\n%s", got)
	}
}

func TestRetractRejectsNonVersions(t *testing.T) {
	for _, v := range []string{"", "1.2.3", "latest", "vX"} {
		if _, _, err := yank.Retract([]byte("module example.com/foo\n"), v, ""); err == nil {
			t.Errorf("Retract(%q) should have failed", v)
		}
	}
}
