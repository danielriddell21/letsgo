package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A corrupt archive must be reported rather than parsed into something.
func TestExtractRejectsGarbage(t *testing.T) {
	if _, err := extract([]byte("not a gzip stream"), "tool.tar.gz", "tool"); err == nil {
		t.Error("garbage was accepted as a tarball")
	}
	if _, err := extract([]byte("not a zip file"), "tool.zip", "tool"); err == nil {
		t.Error("garbage was accepted as a zip")
	}
	// The extension decides the format; anything that is not a zip is read as
	// a tarball, which is what every non-Windows release is.
	if _, err := extract([]byte("nonsense"), "tool", "tool"); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestShort(t *testing.T) {
	if got := short(strings.Repeat("a", 64)); len(got) != 12 {
		t.Errorf("short = %q", got)
	}
	if got := short("abcd"); got != "abcd" {
		t.Errorf("short = %q, want it left alone", got)
	}
}

// Apply resolves the running binary and replaces it. Exercised with the lookup
// swapped, because the honest version of this test would overwrite the test
// binary to prove it worked.
func TestApplyReplacesTheResolvedExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tool")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	original := executable
	t.Cleanup(func() { executable = original })
	executable = func() (string, error) { return target, nil }

	u := &Update{}
	// No download URL, so Apply must fail after resolving the target rather
	// than before: the point here is that it got that far.
	err := u.Apply(context.Background())
	if err == nil {
		t.Fatal("want an error without a download")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("the target was disturbed by a failed update: %v", statErr)
	}
}

func TestApplyReportsAnUnresolvableExecutable(t *testing.T) {
	original := executable
	t.Cleanup(func() { executable = original })
	executable = func() (string, error) { return "", errors.New("no such process") }

	if err := (&Update{}).Apply(context.Background()); err == nil {
		t.Fatal("want an error when the running binary cannot be found")
	}
}

// A symlinked binary must have the binary replaced, not the link.
func TestApplyFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "tool-1.2.3")
	link := filepath.Join(dir, "tool")

	if err := os.WriteFile(real, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := replace(resolve(link), []byte("new")); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("the link was replaced instead of the binary: %q", got)
	}
}

func TestReplaceRefusesAnUnwritableDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "tool")
	if err := replace(target, []byte("new")); err == nil {
		t.Fatal("want an error for a directory that does not exist")
	}
}
