package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTagAndWorktreeLifecycle(t *testing.T) {
	// Annotated tags need a committer identity, which CI runners lack.
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	ctx := context.Background()
	bin := testGit(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	head, err := runner(bin, dir).Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	if runner(bin, dir).TagExists(ctx, "v1.0.0") {
		t.Fatal("TagExists before the tag was made")
	}
	if err := runner(bin, dir).CreateTag(ctx, "v1.0.0", "v1.0.0"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if err := runner(bin, dir).CreateTag(ctx, "v1.0.0", "v1.0.0"); err == nil {
		t.Error("CreateTag accepted a duplicate tag")
	}
	if err := runner(bin, dir).CreateTagAt(ctx, "v1.0.1", head, "v1.0.1"); err != nil {
		t.Fatalf("CreateTagAt: %v", err)
	}
	if err := runner(bin, dir).CreateTagAt(ctx, "v1.0.1", head, "v1.0.1"); err == nil {
		t.Error("CreateTagAt accepted a duplicate tag")
	}
	if got, err := runner(bin, dir).TagCommit(ctx, "v1.0.1"); err != nil || got != head {
		t.Errorf("TagCommit = %q, %v, want %q", got, err, head)
	}

	if err := runner(bin, dir).PushTag(ctx, "origin", "v1.0.1"); err == nil {
		t.Error("PushTag succeeded with no remote")
	}

	work := filepath.Join(t.TempDir(), "wt")
	if err := runner(bin, dir).AddWorktree(ctx, work, head); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := runner(bin, dir).RemoveWorktree(ctx, work); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if err := runner(bin, dir).RemoveWorktree(ctx, work); err == nil {
		t.Error("RemoveWorktree succeeded on a worktree already gone")
	}
	if err := runner(bin, dir).AddWorktree(ctx, work, "deadbeef"); err == nil {
		t.Error("AddWorktree accepted an unknown commit")
	}
}

// A worktree always checks out the whole repository, so a module nested
// inside it has to be compared at <worktree>/relDir, never at the worktree's
// own root, which for a scoped module holds the sibling directories the
// release has nothing to do with.
func TestCheckoutTagReturnsTheModulesOwnDirectory(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	dir := t.TempDir()

	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	write(t, dir, "services/api/go.mod", "module github.com/you/foo/services/api\n\ngo 1.24\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	gitRun(t, dir, "tag", "services/api/v1.0.0")

	old, cleanup, err := runner(testGit(t), dir).CheckoutTag(context.Background(), "services/api/v1.0.0", "services/api")
	if err != nil {
		t.Fatalf("CheckoutTag: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(old, "go.mod"))
	if err != nil {
		t.Fatalf("the returned directory is not the nested module's own: %v", err)
	}
	if string(data) != "module github.com/you/foo/services/api\n\ngo 1.24\n" {
		t.Errorf("go.mod = %q, want the nested module's own", data)
	}

	cleanup()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind (err = %v)", old, err)
	}
}

// A root module's own directory is the worktree's root: relDir is empty, and
// nothing is joined onto it.
func TestCheckoutTagWithNoScopeReturnsTheWorktreeItself(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	dir := t.TempDir()
	write(t, dir, "go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	gitRun(t, dir, "tag", "v1.0.0")

	old, cleanup, err := runner(testGit(t), dir).CheckoutTag(context.Background(), "v1.0.0", "")
	if err != nil {
		t.Fatalf("CheckoutTag: %v", err)
	}
	defer cleanup()

	if _, err := os.ReadFile(filepath.Join(old, "go.mod")); err != nil {
		t.Errorf("the returned directory is not the worktree's root: %v", err)
	}
}

func TestCheckoutTagFailsForAnUnknownTagAndLeavesNothingBehind(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	dir := t.TempDir()
	write(t, dir, "go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")

	if _, _, err := runner(testGit(t), dir).CheckoutTag(context.Background(), "v9.9.9", ""); err == nil {
		t.Fatal("checking out a tag that does not exist should fail")
	}
}

func runner(bin, dir string) Runner { return Runner{Bin: bin, Dir: dir} }
