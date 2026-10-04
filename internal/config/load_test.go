package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/modsyntax"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadReadsAndDecodes(t *testing.T) {
	dir := writeConfig(t, "project foo\n")
	cfg, path, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "foo" || path != filepath.Join(dir, FileName) {
		t.Errorf("Load = %+v from %q", cfg, path)
	}
}

func TestLoadReturnsErrNotFoundOnlyForAMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, path, err := Load(dir)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if path != filepath.Join(dir, FileName) {
		t.Errorf("path = %q, want it set for a missing file", path)
	}
}

func TestLoadReturnsOtherReadErrorsAsIs(t *testing.T) {
	// A directory where the file should be is a read error on every platform
	// and for every user, root included, unlike a permission bit.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(dir)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want a read error that is not ErrNotFound", err)
	}
}

func TestLoadNamesTheFileInSyntaxAndDecodeErrors(t *testing.T) {
	for _, src := range []string{"build (\n", "buidl x\n"} {
		_, _, err := Load(writeConfig(t, src))
		var se *modsyntax.SyntaxError
		if !errors.As(err, &se) || se.File != FileName {
			t.Errorf("%q: err = %v, want a syntax error in %s", src, err, FileName)
		}
	}
}
