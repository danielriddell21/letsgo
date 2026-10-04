package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/github"
)

// moduleRepo bundles what a command needs to act on this module's own
// release: the checkout, its scope within a monorepo, and an authenticated
// forge client.
type moduleRepo struct {
	Module discover.Module
	Git    discover.Git
	GitBin string
	Repo   github.Repo
	Scope  discover.Scope
	Token  string
	Client *github.Client
}

// resolveModuleRepo runs the bootstrapping every command that acts on "the
// current repository's release" (not an arbitrary --repo) needs before it
// can do anything else: find the module, its repository, its git checkout,
// its scope, and a forge client authenticated with token (or the default
// env vars, when token is empty).
func (f forge) resolveModuleRepo(ctx context.Context, token string) (moduleRepo, error) {
	module, err := discover.FindModule(".")
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	gitBin, err := gitBinary()
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	found, err := discover.FindRepo(ctx, gitBin, module.Dir)
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	repo := github.Repo{Owner: found.Owner, Name: found.Name}

	git, err := discover.FindGit(ctx, gitBin, module.Dir)
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	scope, err := discover.NewScope(git.TopLevel, module.Dir)
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}

	tokenValue, _ := plan.Token(ctx, machineConfig(), token)
	if tokenValue == "" {
		return moduleRepo{}, fmt.Errorf("letsgo: no token; set %s", envList())
	}
	client := f.client(tokenValue)

	return moduleRepo{Module: module, Git: git, GitBin: gitBin, Repo: repo, Scope: scope, Token: tokenValue, Client: client}, nil
}

// scratchRun bundles what a command needs to act on a release with an
// optional --repo override: the target repository, its local module
// directory (empty when --repo named a repository outside this checkout),
// its scope prefix, scratch space for extracted source, and a forge client.
type scratchRun struct {
	Repo    github.Repo
	Dir     string
	Prefix  string
	WorkDir string
	Client  *github.Client
	GitBin  string
	cleanup func()
}

// resolveScratchRun is the bootstrapping runVerify and runAudit share: both
// accept an optional --repo (unlike resolveModuleRepo, which always acts on
// this checkout's own release) and tolerate an anonymous client for a
// public repository (unlike resolveModuleRepo, which requires a token).
// workDir is created under a temporary directory named tmpPrefix when work
// is empty; cleanup removes it, and is a no-op when work was given
// explicitly.
func (f forge) resolveScratchRun(ctx context.Context, repoFlag, token, work, tmpPrefix string) (scratchRun, error) {
	gitBin, err := gitBinary()
	if err != nil {
		return scratchRun{}, err
	}
	repo, dir, err := targetRepo(ctx, gitBin, repoFlag)
	if err != nil {
		return scratchRun{}, err
	}
	prefix, err := scopePrefix(ctx, gitBin, repoFlag, dir)
	if err != nil {
		return scratchRun{}, err
	}

	workDir := work
	cleanup := func() {}
	if workDir == "" {
		workDir, err = os.MkdirTemp("", tmpPrefix)
		if err != nil {
			return scratchRun{}, fmt.Errorf("letsgo: scratch directory: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(workDir) }
	}

	tokenValue, _ := plan.Token(ctx, machineConfig(), token)
	client := f.client(tokenValue)

	return scratchRun{Repo: repo, Dir: dir, Prefix: prefix, WorkDir: workDir, Client: client, GitBin: gitBin, cleanup: cleanup}, nil
}

// targetRepo resolves which repository a command is asking about and, where
// possible, a local checkout of it.
//
// Inspecting someone else's release is the point, so a repository outside the
// current directory is allowed; it simply has no local checkout, and the
// callers that need one say so.
func targetRepo(ctx context.Context, gitBin, explicit string) (github.Repo, string, error) {
	if explicit != "" {
		owner, name, ok := strings.Cut(explicit, "/")
		if !ok || owner == "" || name == "" {
			return github.Repo{}, "", fmt.Errorf("--repo must be owner/name, got %q", explicit)
		}
		return github.Repo{Owner: owner, Name: name}, "", nil
	}

	module, err := discover.FindModule(".")
	if err != nil {
		return github.Repo{}, "", fmt.Errorf("%w (use --repo to verify a release elsewhere)", err)
	}
	found, err := discover.FindRepo(ctx, gitBin, module.Dir)
	if err != nil {
		return github.Repo{}, "", err
	}
	return github.Repo{Owner: found.Owner, Name: found.Name}, module.Dir, nil
}

// scopePrefix resolves the module's scope prefix (see discover.Scope), so
// "no tag given" can find the latest release within this module's own
// scope rather than the repository's overall latest — the same distinction
// runYank draws before picking a previous release.
//
// A repository named explicitly by --repo has no local module to scope by:
// inspecting a release elsewhere always means the whole repository.
func scopePrefix(ctx context.Context, gitBin, explicit, dir string) (string, error) {
	if explicit != "" || dir == "" {
		return "", nil
	}
	git, err := discover.FindGit(ctx, gitBin, dir)
	if err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	scope, err := discover.NewScope(git.TopLevel, dir)
	if err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	return scope.Prefix, nil
}

// gitBinary resolves the git command from the machine's global config.
func gitBinary() (string, error) {
	path, _, err := discover.GitBinary(machineConfig())
	return path, err
}
