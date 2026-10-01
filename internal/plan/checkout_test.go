package plan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func checkoutGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A worktree always checks out the whole repository, so a module nested
// inside it has to be compared at <worktree>/relDir — never at the
// worktree's own root, which for a scoped module holds the sibling
// directories the release has nothing to do with.
func TestCheckoutTagReturnsTheModulesOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	checkoutGitRun(t, dir, "init", "-q", "-b", "main")
	write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	write("services/api/go.mod", "module github.com/you/foo/services/api\n\ngo 1.24\n")
	checkoutGitRun(t, dir, "add", ".")
	checkoutGitRun(t, dir, "commit", "-q", "-m", "first")
	checkoutGitRun(t, dir, "tag", "services/api/v1.0.0")

	ctx := context.Background()
	old, cleanup, err := checkoutTag(ctx, "git", dir, "services/api/v1.0.0", "services/api")
	if err != nil {
		t.Fatalf("checkoutTag: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(old, "go.mod"))
	if err != nil {
		t.Fatalf("the returned directory is not the nested module's own: %v", err)
	}
	if string(data) != "module github.com/you/foo/services/api\n\ngo 1.24\n" {
		t.Errorf("go.mod = %q, want the nested module's own", data)
	}
}

// A root module's own directory is the worktree's root: relDir is empty, and
// nothing is joined onto it.
func TestCheckoutTagWithNoScopeReturnsTheWorktreeItself(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/you/foo\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkoutGitRun(t, dir, "init", "-q", "-b", "main")
	checkoutGitRun(t, dir, "add", ".")
	checkoutGitRun(t, dir, "commit", "-q", "-m", "first")
	checkoutGitRun(t, dir, "tag", "v1.0.0")

	ctx := context.Background()
	old, cleanup, err := checkoutTag(ctx, "git", dir, "v1.0.0", "")
	if err != nil {
		t.Fatalf("checkoutTag: %v", err)
	}
	defer cleanup()

	if _, err := os.ReadFile(filepath.Join(old, "go.mod")); err != nil {
		t.Errorf("the returned directory is not the worktree's root: %v", err)
	}
}
