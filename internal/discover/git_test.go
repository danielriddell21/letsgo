package discover

import (
	"context"
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
	head, err := git(ctx, bin, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	if TagExists(ctx, bin, dir, "v1.0.0") {
		t.Fatal("TagExists before the tag was made")
	}
	if err := CreateTag(ctx, bin, dir, "v1.0.0", "v1.0.0"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if err := CreateTag(ctx, bin, dir, "v1.0.0", "v1.0.0"); err == nil {
		t.Error("CreateTag accepted a duplicate tag")
	}
	if err := CreateTagAt(ctx, bin, dir, "v1.0.1", head, "v1.0.1"); err != nil {
		t.Fatalf("CreateTagAt: %v", err)
	}
	if err := CreateTagAt(ctx, bin, dir, "v1.0.1", head, "v1.0.1"); err == nil {
		t.Error("CreateTagAt accepted a duplicate tag")
	}
	if got, err := TagCommit(ctx, bin, dir, "v1.0.1"); err != nil || got != head {
		t.Errorf("TagCommit = %q, %v, want %q", got, err, head)
	}

	if err := PushTag(ctx, bin, dir, "origin", "v1.0.1"); err == nil {
		t.Error("PushTag succeeded with no remote")
	}

	work := filepath.Join(t.TempDir(), "wt")
	if err := AddWorktree(ctx, bin, dir, work, head); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := RemoveWorktree(ctx, bin, dir, work); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if err := RemoveWorktree(ctx, bin, dir, work); err == nil {
		t.Error("RemoveWorktree succeeded on a worktree already gone")
	}
	if err := AddWorktree(ctx, bin, dir, work, "deadbeef"); err == nil {
		t.Error("AddWorktree accepted an unknown commit")
	}
}
