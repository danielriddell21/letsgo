package discover

import (
	"context"

	"github.com/danielriddell21/letsgo/internal/git"
)

// Location is where a module sits: the module itself, the git checkout it is
// in, its scope within a monorepo, and the forge repository its origin names.
//
// It is what every command that acts on "this module's release" needs before
// it can do anything else, found once rather than once per command.
type Location struct {
	Module Module

	// Git is the state of the checkout at HEAD, and Runner runs git in the
	// module's directory with the binary Locate was given.
	Git    git.State
	Runner git.Runner

	// Scope is the module's own place in the repository, which prefixes its
	// tags in a monorepo.
	Scope Scope

	// Repo is the forge repository the origin remote names. RepoErr says why
	// there is none: a repository with no origin is an ordinary state for a
	// local build, so it is not an error from Locate itself.
	Repo    Repo
	RepoErr error
}

// HasRepo reports whether the origin remote named a forge repository.
func (l Location) HasRepo() bool { return l.RepoErr == nil }

// Locate finds the module containing start, its git checkout, scope and
// repository. gitBin is the resolved git binary (see git.Binary).
//
// Errors are returned as they are found: the module not being found, git not
// seeing a repository with a commit, or a scope that cannot be derived. A
// missing origin is not one of them; see Location.RepoErr.
func Locate(ctx context.Context, gitBin, start string) (Location, error) {
	module, err := FindModule(start)
	if err != nil {
		return Location{}, err
	}

	runner := git.Runner{Bin: gitBin, Dir: module.Dir}
	state, err := runner.State(ctx)
	if err != nil {
		return Location{}, err
	}

	scope, err := NewScope(state.TopLevel, module.Dir)
	if err != nil {
		return Location{}, err
	}

	loc := Location{Module: module, Git: state, Runner: runner, Scope: scope}
	loc.Repo, loc.RepoErr = FindRepo(ctx, runner)
	return loc, nil
}
