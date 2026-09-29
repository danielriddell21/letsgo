package doctor_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/doctor"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// gitRun runs a git command in dir with a fixed test identity, so a commit
// never depends on the runner's own (possibly absent) global git config.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitInit makes dir a git repository with one commit, so doctor.Run's
// discover.FindGit has a repository to inspect.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
}

func TestReportFormatsChecksGroupedInOrder(t *testing.T) {
	r := &doctor.Result{Checks: []doctor.Check{
		{Group: "tools", Name: "go", Status: doctor.OK, Detail: "go1.24.0 (/usr/bin/go)"},
		{Group: "tools", Name: "govulncheck", Status: doctor.Warn, Detail: "not installed", Hint: "go install golang.org/x/vuln/cmd/govulncheck@latest"},
		{Group: "repository", Name: "letsgo.mod", Status: doctor.Fail, Detail: "invalid"},
	}}

	var buf strings.Builder
	r.Report(&buf)
	out := buf.String()

	for _, want := range []string{
		"tools", "✓ go", "go1.24.0 (/usr/bin/go)",
		"! govulncheck", "not installed — go install golang.org/x/vuln/cmd/govulncheck@latest",
		"repository", "✗ letsgo.mod", "invalid",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Report output missing %q; got:\n%s", want, out)
		}
	}
}

func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunReportsGoAndGit(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")
	gitInit(t, dir)

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
	gitInit(t, dir)

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
	gitInit(t, dir)

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

func writeLetsgoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "letsgo.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkByName(t *testing.T, result *doctor.Result, name string) doctor.Check {
	t.Helper()
	for _, c := range result.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in result", name)
	return doctor.Check{}
}

func TestRunFailsOnAMalformedLetsgoMod(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")
	writeLetsgoMod(t, dir, "this is not a directive at all !!!\n")
	gitInit(t, dir)

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := checkByName(t, result, "letsgo.mod")
	if c.Status != doctor.Fail {
		t.Errorf("letsgo.mod check status = %v, want Fail", c.Status)
	}
	if !strings.Contains(c.Detail, "letsgo.mod:1:") {
		t.Errorf("letsgo.mod check detail = %q, want a file:line:col position", c.Detail)
	}
	if result.OK() {
		t.Error("OK() = true, want false with a malformed letsgo.mod")
	}
}

func TestRunReportsAMissingPluginPin(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")
	writeLetsgoMod(t, dir, "plugin ldflags letsgo-doctor-test-nonexistent-plugin v1.0.0 "+
		"sha256:"+strings.Repeat("0", 64)+"\n")
	gitInit(t, dir)

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := checkByName(t, result, "plugin")
	if c.Status != doctor.Fail {
		t.Errorf("plugin check status = %v, want Fail", c.Status)
	}
	if !strings.Contains(c.Detail, "not installed") {
		t.Errorf("plugin check detail = %q, want it to say not installed", c.Detail)
	}
}

func TestRunReportsANonGitHubOrigin(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")
	gitInit(t, dir)
	gitRun(t, dir, "remote", "add", "origin", "https://gitlab.com/you/doctortest.git")

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := checkByName(t, result, "remote")
	if c.Status != doctor.Warn {
		t.Errorf("remote check status = %v, want Warn", c.Status)
	}
	if !strings.Contains(c.Detail, "gitlab.com") {
		t.Errorf("remote check detail = %q, want it to name gitlab.com", c.Detail)
	}
}

func TestRunReportsADirtyWorktree(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n")
	gitInit(t, dir)
	writeGoMod(t, dir, "module example.com/doctortest\n\ngo 1.24\n// dirty\n")

	result, err := doctor.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := checkByName(t, result, "worktree")
	if c.Status != doctor.Warn {
		t.Errorf("worktree check status = %v, want Warn", c.Status)
	}
}

func TestRunReportsAShallowClone(t *testing.T) {
	src := t.TempDir()
	writeGoMod(t, src, "module example.com/doctortest\n\ngo 1.24\n")
	gitInit(t, src)
	// A second commit so the shallow clone below (depth 1) is provably
	// shallower than the source's own history.
	writeGoMod(t, src, "module example.com/doctortest\n\ngo 1.24\n// v2\n")
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-q", "-m", "second")

	dir := t.TempDir()
	shallow := filepath.Join(dir, "clone")
	cmd := exec.Command("git", "clone", "--depth", "1", "file://"+src, shallow)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}

	result, err := doctor.Run(context.Background(), shallow)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := checkByName(t, result, "history")
	if c.Status != doctor.Warn {
		t.Errorf("history check status = %v, want Warn", c.Status)
	}
	if c.Hint != "use fetch-depth: 0" {
		t.Errorf("history check hint = %q, want the fetch-depth hint", c.Hint)
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
