package bump

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// Repo is the module in a git checkout a version is proposed for.
type Repo struct {
	GitBin string
	Module discover.Module
	Scope  discover.Scope

	// Global is the machine's config, which may pin the Go toolchain the API
	// comparison runs with. Nil means none does.
	Global *config.Global
}

// ProposeFor gathers both signals for the repository and combines them with
// Propose. previous is the real git tag, prefixed exactly as it exists in the
// repository (the commit walk and the checkout need that to resolve it), but
// the version Propose computes is a plain "vX.Y.Z", so the scope prefix is
// stripped before it reaches the semver parser.
//
// A non-None force replaces the signals outright: the caller has decided.
func (r Repo) ProposeFor(ctx context.Context, previous string, force Level) (Proposal, error) {
	previousVersion := strings.TrimPrefix(previous, r.Scope.Prefix)

	if force != None {
		return Propose(previousVersion, r.Module.Path,
			Signal{Source: "you", Level: force, Detail: "requested on the command line"})
	}

	nested, err := discover.NestedModuleDirs(r.Module.Dir)
	if err != nil {
		return Proposal{}, err
	}
	commits, err := discover.Commits(ctx, r.GitBin, r.Module.Dir, previous, "HEAD", nested...)
	if err != nil {
		return Proposal{}, err
	}
	notes := changelog.Build(previous, "", commits)

	// The API signal needs an earlier tree to compare against, and something
	// importable to compare. Whatever stopped it is carried into the signal
	// rather than swallowed: a signal that dropped out leaves the version
	// decided by commit messages alone, and the report should say so.
	var (
		changes []gate.Change
		apiErr  = errors.New("no earlier release to compare against")
	)
	if previous != "" {
		goBin, _, _ := gobuild.Toolchain(r.Global)
		old, cleanup, err := discover.CheckoutTag(ctx, r.GitBin, r.Module.Dir, previous, r.Scope.Dir)
		if err != nil {
			apiErr = err
		} else {
			defer cleanup()
			changes, apiErr = gate.APIDiff(ctx, r.Global, goBin, old, r.Module.Dir)
		}
	}

	return Propose(previousVersion, r.Module.Path,
		FromAPI(changes, apiErr),
		FromCommits(notes.Entries))
}

// Forced is the level a flag demands, or None for no flag. The largest wins
// when more than one is set.
func Forced(major, minor, patch bool) Level {
	switch {
	case major:
		return Major
	case minor:
		return Minor
	case patch:
		return Patch
	default:
		return None
	}
}

// NextPrerelease appends an auto-incrementing "-rc.N" suffix to a proposed
// base version. It scans the existing tags for the highest N already used
// under that exact base, so repeated prerelease runs advance rc.1, rc.2, ...
// instead of colliding on the same candidate.
func NextPrerelease(tags []string, prefix, base string) string {
	want := prefix + base + "-rc."
	n := 0
	for _, tag := range tags {
		suffix, ok := strings.CutPrefix(tag, want)
		if !ok {
			continue
		}
		if v, err := strconv.Atoi(suffix); err == nil && v > n {
			n = v
		}
	}
	return fmt.Sprintf("%s-rc.%d", base, n+1)
}
