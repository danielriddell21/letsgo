package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	stdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		_, _ = io.WriteString(w, content)
		w.Close()
	}()
	fn()
	os.Stdin = stdin
}

func TestRunFmtDashReadsStdinAndWritesStdout(t *testing.T) {
	const unformatted = "build   linux/amd64\n"

	var out string
	withStdin(t, unformatted, func() {
		out = captureStdout(t, func() {
			if err := runFmt([]string{"-"}); err != nil {
				t.Fatalf("runFmt: %v", err)
			}
		})
	})

	if !strings.Contains(out, "build linux/amd64\n") {
		t.Errorf("runFmt - output = %q", out)
	}
}

func TestRunFmtDashDoesNotWriteAFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	withStdin(t, "build linux/amd64\n", func() {
		captureStdout(t, func() {
			if err := runFmt([]string{"-"}); err != nil {
				t.Fatalf("runFmt: %v", err)
			}
		})
	})

	if _, err := os.Stat(filepath.Join(dir, "letsgo.mod")); !os.IsNotExist(err) {
		t.Errorf("runFmt - wrote letsgo.mod to disk: %v", err)
	}
}

func TestRunFmtDashFailsOnAParseError(t *testing.T) {
	withStdin(t, "budget\n", func() {
		captureStdout(t, func() {
			if err := runFmt([]string{"-"}); err == nil {
				t.Fatal("runFmt - accepted an unparseable file")
			}
		})
	})
}

// ED-5: formatting through stdin must equal file-mode formatting byte for
// byte, since the editor's format-on-save has to match `letsgo fmt` exactly.
func TestRunFmtDashMatchesFileModeByteForByte(t *testing.T) {
	const unformatted = "build   linux/amd64\n\nbudget linux/amd64   15MB\n"

	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "letsgo.mod")
	if err := os.WriteFile(path, []byte(unformatted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runFmt(nil); err != nil {
		t.Fatalf("runFmt: %v", err)
	}
	wantFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var out string
	withStdin(t, unformatted, func() {
		out = captureStdout(t, func() {
			if err := runFmt([]string{"-"}); err != nil {
				t.Fatalf("runFmt -: %v", err)
			}
		})
	})

	if out != string(wantFile) {
		t.Errorf("runFmt - output = %q, want %q", out, string(wantFile))
	}
}
