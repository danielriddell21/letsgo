package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}
	path := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSmokeAcceptsMatchingOutput(t *testing.T) {
	bin := script(t, `echo "foo 1.2.3 (9f2ab1c) built 2024-03-15"`+"\n")
	if _, err := runSmoke(context.Background(), bin, Smoke{Want: "1.2.3"}); err != nil {
		t.Errorf("runSmoke: %v", err)
	}
}

// The case the structural symbol check cannot catch: the linker wrote the
// value and package initialisation overwrote it.
func TestSmokeRejectsWrongVersion(t *testing.T) {
	bin := script(t, `echo "foo dev"`+"\n")

	_, err := runSmoke(context.Background(), bin, Smoke{Want: "1.2.3"})
	if err == nil {
		t.Fatal("runSmoke accepted a binary reporting the wrong version")
	}
	// The message has to explain the likely cause, or the reader is left
	// staring at a version string wondering what letsgo wanted.
	for _, want := range []string{"dev", "1.2.3", "overwrite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%s", want, err)
		}
	}
}

// A binary that panics on startup must never reach the rest of the matrix.
func TestSmokeRejectsCrash(t *testing.T) {
	bin := script(t, "echo 'panic: nil map' >&2\nexit 2\n")

	_, err := runSmoke(context.Background(), bin, Smoke{Want: "1.2.3"})
	if err == nil {
		t.Fatal("runSmoke accepted a binary that crashed")
	}
	if !strings.Contains(err.Error(), "panic: nil map") {
		t.Errorf("error does not include the binary's output:\n%s", err)
	}
	if !strings.Contains(err.Error(), "crashed") {
		t.Errorf("error does not name the problem:\n%s", err)
	}
}

// Not every binary has a --version flag. Its non-zero exit is
// indistinguishable from a crash by status alone, so failing on it would
// block releases over a design choice rather than a defect.
func TestSmokeWarnsRatherThanFailsOnMissingFlag(t *testing.T) {
	bin := script(t, "echo 'unknown flag: --version' >&2\nexit 2\n")

	warning, err := runSmoke(context.Background(), bin, Smoke{Want: "1.2.3"})
	if err != nil {
		t.Fatalf("runSmoke failed a binary that merely lacks the flag: %v", err)
	}
	if warning == "" {
		t.Fatal("runSmoke reported nothing; the version was not confirmed and that is worth saying")
	}
	if !strings.Contains(warning, "status 2") {
		t.Errorf("warning does not say what happened: %q", warning)
	}
}

// A Go program that dies mid-run is a crash however it is spelled.
func TestSmokeRecognisesCrashForms(t *testing.T) {
	for _, output := range []string{
		"fatal error: concurrent map writes",
		"panic: runtime error: index out of range",
		"runtime error: invalid memory address",
	} {
		t.Run(output[:12], func(t *testing.T) {
			bin := script(t, "echo '"+output+"' >&2\nexit 2\n")
			if _, err := runSmoke(context.Background(), bin, Smoke{Want: "x"}); err == nil {
				t.Errorf("runSmoke accepted %q", output)
			}
		})
	}
}

// Printing a version to stderr is unusual but not wrong; failing it would be
// a false alarm.
func TestSmokeReadsStderr(t *testing.T) {
	bin := script(t, "echo 'foo 1.2.3' >&2\n")
	if _, err := runSmoke(context.Background(), bin, Smoke{Want: "1.2.3"}); err != nil {
		t.Errorf("runSmoke rejected output on stderr: %v", err)
	}
}

// A binary that waits for input would otherwise hang the release.
func TestSmokeTimesOut(t *testing.T) {
	bin := script(t, "sleep 30\n")

	started := time.Now()
	_, err := runSmoke(context.Background(), bin, Smoke{Want: "x", Timeout: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("runSmoke accepted a binary that never exited")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("runSmoke waited %s, want the timeout to apply", elapsed)
	}
	if !strings.Contains(err.Error(), "did not exit") {
		t.Errorf("error does not mention the timeout:\n%s", err)
	}
}

func TestSmokeDefaultsToVersionFlag(t *testing.T) {
	bin := script(t, `[ "$1" = "--version" ] && echo "ok 1.2.3" || echo "wrong args"`+"\n")
	if _, err := runSmoke(context.Background(), bin, Smoke{Want: "ok 1.2.3"}); err != nil {
		t.Errorf("runSmoke did not default to --version: %v", err)
	}
}

func TestSmokeWithoutWantOnlyChecksItRuns(t *testing.T) {
	bin := script(t, "echo anything\n")
	if _, err := runSmoke(context.Background(), bin, Smoke{}); err != nil {
		t.Errorf("runSmoke: %v", err)
	}
}
