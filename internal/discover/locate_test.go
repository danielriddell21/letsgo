package discover

import (
	"context"
	"path/filepath"
	"testing"
)

// repository writes a module with an origin and one commit under a nested
// directory of a repository, the shape a monorepo release has.
func repository(t *testing.T, origin string) (repoDir, moduleDir string) {
	t.Helper()
	repoDir = t.TempDir()
	moduleDir = filepath.Join(repoDir, "services", "api")

	write(t, repoDir, "go.mod", "module example.com/foo\n\ngo 1.24\n")
	write(t, repoDir, "services/api/go.mod", "module example.com/foo/services/api\n\ngo 1.24\n")
	write(t, repoDir, "services/api/internal/x.go", "package internal\n")
	gitRun(t, repoDir, "init", "-q", "-b", "main")
	if origin != "" {
		gitRun(t, repoDir, "remote", "add", "origin", origin)
	}
	gitRun(t, repoDir, "add", ".")
	gitRun(t, repoDir, "commit", "-q", "-m", "first")
	return repoDir, moduleDir
}

func TestLocateFindsTheModuleItsCheckoutItsScopeAndItsRepository(t *testing.T) {
	_, moduleDir := repository(t, "git@github.com:you/foo.git")

	// Started from a directory below the module, as a command run in a
	// subdirectory is.
	start := filepath.Join(moduleDir, "internal")

	loc, err := Locate(context.Background(), testGit(t), start)
	if err != nil {
		t.Fatalf("Locate = %v", err)
	}

	if loc.Module.Path != "example.com/foo/services/api" || loc.Module.Dir != moduleDir {
		t.Errorf("Module = %+v", loc.Module)
	}
	if loc.Scope.Prefix != "services/api/" || loc.Scope.Dir != "services/api" {
		t.Errorf("Scope = %+v, want the module's own directory", loc.Scope)
	}
	if loc.Git.Commit == "" || !loc.Git.Clean {
		t.Errorf("Git = %+v, want a clean checkout with a commit", loc.Git)
	}
	if !loc.HasRepo() || loc.Repo.Owner != "you" || loc.Repo.Name != "foo" || loc.Repo.Host != "github.com" {
		t.Errorf("Repo = %+v, err %v", loc.Repo, loc.RepoErr)
	}
	if loc.Runner.Dir != moduleDir || loc.Runner.Bin != testGit(t) {
		t.Errorf("Runner = %+v, want git in the module's directory", loc.Runner)
	}
}

// A repository with no origin is an ordinary state for a local build: Locate
// succeeds and says why there is no repository, so the caller decides whether
// that matters.
func TestLocateToleratesAMissingOrigin(t *testing.T) {
	_, moduleDir := repository(t, "")

	loc, err := Locate(context.Background(), testGit(t), moduleDir)
	if err != nil {
		t.Fatalf("Locate = %v, want success without an origin", err)
	}
	if loc.HasRepo() || loc.RepoErr == nil {
		t.Errorf("HasRepo = %v, RepoErr = %v, want a reason there is no repository", loc.HasRepo(), loc.RepoErr)
	}
}

func TestLocateReportsTheFirstThingItCannotFind(t *testing.T) {
	ctx := context.Background()

	t.Run("no module", func(t *testing.T) {
		if _, err := Locate(ctx, testGit(t), t.TempDir()); err == nil {
			t.Error("Locate succeeded with no go.mod above the directory")
		}
	})

	t.Run("a module outside any repository", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		if _, err := Locate(ctx, testGit(t), dir); err == nil {
			t.Error("Locate succeeded outside a git repository")
		}
	})

	t.Run("a repository with no commits", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		gitRun(t, dir, "init", "-q", "-b", "main")
		if _, err := Locate(ctx, testGit(t), dir); err == nil {
			t.Error("Locate succeeded in a repository with no commits")
		}
	})
}

func TestLocateReportsARemoteThatNamesNoRepository(t *testing.T) {
	_, moduleDir := repository(t, "https://example.test/not-a-forge-url")

	loc, err := Locate(context.Background(), testGit(t), moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if loc.HasRepo() {
		t.Errorf("a remote that names no repository was accepted: %+v", loc.Repo)
	}
}
