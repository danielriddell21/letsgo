package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitRun runs git in dir with a fixed author/committer identity and date, so
// tests built on it are deterministic.
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

func TestState(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")
	run("remote", "add", "origin", "git@github.com:you/foo.git")

	ctx := context.Background()

	g, err := runner(testGit(t), dir).State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !g.Clean {
		t.Error("worktree should be clean")
	}
	if len(g.Tags) != 1 || g.Tags[0] != "v1.0.0" {
		t.Errorf("Tags = %v, want [v1.0.0]", g.Tags)
	}
	if g.CommitTime.UTC().Format("2006-01-02T15:04:05Z") != "2024-03-15T12:30:45Z" {
		t.Errorf("CommitTime = %s, want the committer date", g.CommitTime)
	}
	if len(g.Commit) != 40 {
		t.Errorf("Commit = %q, want a full sha", g.Commit)
	}
	if !strings.HasPrefix(g.Commit, g.ShortCommit) {
		t.Errorf("ShortCommit %q is not a prefix of %q", g.ShortCommit, g.Commit)
	}
	if g.Shallow {
		t.Error("a fresh repository should not be shallow")
	}

	url, err := runner(testGit(t), dir).Remote(ctx, "origin")
	if err != nil {
		t.Fatalf("Remote: %v", err)
	}
	if !strings.Contains(url, "you/foo") {
		t.Errorf("Remote = %q, want the origin URL", url)
	}

	// A tag on HEAD is the release being made, not the one before it.
	if prev, err := runner(testGit(t), dir).PreviousTag(ctx, ""); err != nil || prev != "" {
		t.Errorf("PreviousTag = %q, %v; want empty for a first release", prev, err)
	}

	write(t, dir, "README.md", "changed\n")
	dirty, err := runner(testGit(t), dir).State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Clean {
		t.Error("worktree should be dirty after an edit")
	}

	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "v1.1.0")

	if prev, err := runner(testGit(t), dir).PreviousTag(ctx, ""); err != nil || prev != "v1.0.0" {
		t.Errorf("PreviousTag = %q, %v; want v1.0.0", prev, err)
	}
}

// Scope depends on this: a root module derived against its own TopLevel must
// come out empty, or every plain repository would carry a prefix. So the
// TopLevel is the repository root, with symlinks resolved the way the module
// directory is.
func TestStateReportsATopLevelThatAgreesWithTheDirectory(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")

	g, err := runner(testGit(t), dir).State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// git writes forward slashes even on Windows; the directory is native.
	if got := filepath.FromSlash(g.TopLevel); got != want {
		t.Errorf("TopLevel = %q, want the repository's own directory %q", g.TopLevel, want)
	}
}

func TestPreviousTagIgnoresPrefixedTags(t *testing.T) {
	dir := repoWithNestedModuleTag(t)
	run := func(args ...string) { gitRun(t, dir, args...) }

	write(t, dir, "README.md", "changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")

	ctx := context.Background()
	if prev, err := runner(testGit(t), dir).PreviousTag(ctx, ""); err != nil || prev != "v1.0.0" {
		t.Errorf("PreviousTag = %q, %v; want v1.0.0, not the nested module's tag", prev, err)
	}
}

// Unlike PreviousTag, Tags returns every matching tag reachable from HEAD,
// not just the nearest one, so a version-order rule can be applied on top.
func TestTagsReturnsEveryMatchingTagInScope(t *testing.T) {
	dir := repoWithNestedModuleTag(t)
	run := func(args ...string) { gitRun(t, dir, args...) }
	run("tag", "v1.1.0-rc.1")

	write(t, dir, "README.md", "changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")
	run("tag", "v1.1.0")

	ctx := context.Background()
	got, err := runner(testGit(t), dir).Tags(ctx, "")
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	want := map[string]bool{"v1.0.0": true, "v1.1.0-rc.1": true, "v1.1.0": true}
	if len(got) != len(want) {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
	for _, tag := range got {
		if !want[tag] {
			t.Errorf("Tags returned unexpected tag %q (web's tag should have been excluded)", tag)
		}
	}
}

func TestTagsIsEmptyNotAnErrorForAFirstRelease(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")

	got, err := runner(testGit(t), dir).Tags(context.Background(), "")
	if err != nil || got != nil {
		t.Errorf("Tags = %v, %v; want nil, nil", got, err)
	}
}

// The pathspec Commits builds from exclude must actually exclude, or the
// nested module's own commits leak into a history they should never appear
// in.
func TestCommitsExcludesTheGivenDirectories(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")

	write(t, dir, "services/api/main.go", "package main\n")
	run("add", ".")
	run("commit", "-q", "-m", "second: touches the nested module only")

	write(t, dir, "README.md", "changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "third: touches the root")

	ctx := context.Background()
	commits, err := runner(testGit(t), dir).Commits(ctx, "", "HEAD", "services/api")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}
	for _, c := range commits {
		if strings.Contains(c.Subject, "second") {
			t.Errorf("the excluded directory's commit was not excluded: %+v", commits)
		}
	}
	if len(commits) != 2 {
		t.Errorf("got %d commits, want 2 (first and third)", len(commits))
	}
}

// repoWithNestedModuleTag is a repository with a root release tag and a
// nested "web" module carrying its own web/v1.0.0 tag, the shared starting
// point every test proving a nested module's tags stay out of the root's own
// answer builds on.
func repoWithNestedModuleTag(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")

	write(t, dir, "web/go.mod", "module example.com/foo/web\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "web/v1.0.0")

	return dir
}
