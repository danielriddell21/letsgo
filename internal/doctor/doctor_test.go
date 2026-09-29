package doctor_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/doctor"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunReportsGoAndGit(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	byName := make(map[string]doctor.Check, len(result.Checks))
	for _, c := range result.Checks {
		byName[c.Name] = c
	}

	for _, name := range []string{"go", "git", "govulncheck", "apidiff"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing a %q check", name)
		}
	}
	if c := byName["go"]; c.Status == doctor.Fail {
		t.Errorf("go check failed: %s", c.Detail)
	}
	if c := byName["git"]; c.Status == doctor.Fail {
		t.Errorf("git check failed: %s", c.Detail)
	}
}

// A two-part go directive only pins the major.minor, and this repository's
// own go.mod (go 1.24 or newer) is satisfied by whatever patch is on this
// machine, so no go.mod check should appear at all here.
func TestRunReportsNoGoModCheckWhenTheDirectiveIsSatisfied(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range result.Checks {
		if c.Name == "go.mod" {
			t.Errorf("unexpected go.mod check: %+v", c)
		}
	}
}

func TestRunWarnsWhenGoModWantsADifferentVersion(t *testing.T) {
	local, err := gobuild.Version(context.Background(), "")
	if err != nil {
		t.Fatalf("gobuild.Version: %v", err)
	}
	// Appending a digit to the real patch version guarantees a three-part
	// directive that cannot match whatever go is actually installed, without
	// hardcoding a version number this test would need updating for later.
	want := strings.TrimPrefix(local, "go") + "0"

	dir := t.TempDir()
	writeGoMod(t, dir, fmt.Sprintf("module example.com/doctortest\n\ngo %s\n", want))

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range result.Checks {
		if c.Name == "go.mod" {
			if c.Status != doctor.Warn {
				t.Errorf("go.mod check status = %v, want Warn", c.Status)
			}
			return
		}
	}
	t.Error("expected a go.mod check when go.mod's version differs from the local go")
}

func TestRunFailsWithNoModule(t *testing.T) {
	dir := t.TempDir()
	if _, err := doctor.Run(context.Background(), dir); err == nil {
		t.Error("want an error with no go.mod present")
	}
}

func TestResultOK(t *testing.T) {
	pass := &doctor.Result{Checks: []doctor.Check{{Status: doctor.OK}, {Status: doctor.Warn}}}
	if !pass.OK() {
		t.Error("OK() = false, want true: Warn alone must not fail the run")
	}

	fail := &doctor.Result{Checks: []doctor.Check{{Status: doctor.OK}, {Status: doctor.Fail}}}
	if fail.OK() {
		t.Error("OK() = true, want false with a Fail check present")
	}
}
