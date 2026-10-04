package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/github"
)

// moduleRepo bundles what a command needs to act on this module's own
// release: the checkout, its scope within a monorepo, and an authenticated
// forge client.
type moduleRepo struct {
	Module discover.Module
	Git    git.State
	GitBin string
	Repo   github.Repo
	Scope  discover.Scope
	Token  string
	Client *github.Client
}

// resolveModuleRepo runs the bootstrapping every command that acts on "the
// current repository's release" (not an arbitrary --repo) needs before it
// can do anything else: find the module, its repository, its git checkout,
// its scope, and a forge client authenticated with the forge credential the
// caller resolved.
func (f forge) resolveModuleRepo(ctx context.Context, cred credential.Credential) (moduleRepo, error) {
	gitBin, err := gitBinary()
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	loc, err := discover.Locate(ctx, gitBin, ".")
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	if !loc.HasRepo() {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", loc.RepoErr)
	}

	tokenValue := cred.Value
	if tokenValue == "" {
		return moduleRepo{}, fmt.Errorf("letsgo: no token; set %s", envList())
	}
	client := f.client(tokenValue)

	return moduleRepo{
		Module: loc.Module, Git: loc.Git, GitBin: gitBin,
		Repo:  github.Repo{Owner: loc.Repo.Owner, Name: loc.Repo.Name},
		Scope: loc.Scope, Token: tokenValue, Client: client,
	}, nil
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
	t, err := resolveTarget(ctx, gitBin, repoFlag, ".")
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

	client := f.client(forgeToken(ctx, token))

	return scratchRun{Repo: t.Repo, Dir: t.Dir, Prefix: t.Prefix, WorkDir: workDir, Client: client, GitBin: gitBin, cleanup: cleanup}, nil
}

// target is the repository a command is asking about and, where there is one,
// the local module it is about.
type target struct {
	Repo github.Repo

	// Dir is the module's directory, empty when --repo named a repository
	// outside this checkout.
	Dir string

	// Prefix is the module's scope prefix (see discover.Scope), so "no tag
	// given" can find the latest release within this module's own scope rather
	// than the repository's overall latest, the same distinction runYank draws
	// before picking a previous release. It is empty for a repository named by
	// --repo: inspecting a release elsewhere always means the whole repository.
	Prefix string
}

// resolveTarget resolves which repository a command is asking about and, where
// possible, a local checkout of it, found from start.
//
// Inspecting someone else's release is the point, so a repository outside the
// current directory is allowed; it simply has no local checkout, and the
// callers that need one say so.
func resolveTarget(ctx context.Context, gitBin, explicit, start string) (target, error) {
	if explicit != "" {
		owner, name, ok := strings.Cut(explicit, "/")
		if !ok || owner == "" || name == "" {
			return target{}, fmt.Errorf("--repo must be owner/name, got %q", explicit)
		}
		return target{Repo: github.Repo{Owner: owner, Name: name}}, nil
	}

	loc, err := discover.Locate(ctx, gitBin, start)
	if err != nil {
		return target{}, fmt.Errorf("%w (use --repo to verify a release elsewhere)", err)
	}
	if !loc.HasRepo() {
		return target{}, loc.RepoErr
	}
	return target{
		Repo: github.Repo{Owner: loc.Repo.Owner, Name: loc.Repo.Name}, Dir: loc.Module.Dir, Prefix: loc.Scope.Prefix,
	}, nil
}

// gitBinary resolves the git command from the machine's global config.
func gitBinary() (string, error) {
	path, _, err := git.Binary(machineConfig())
	return path, err
}
