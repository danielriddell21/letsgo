package main

import (
	"context"
	"fmt"

	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// tapTokenUsage documents --tap-token once, for the three commands that reach
// a tap.
const tapTokenUsage = "token the Homebrew tap is written with (default: $LETSGO_TAP_TOKEN, else the release token)"

// releaseTokenUsage documents --release-token once.
const releaseTokenUsage = "token the GitHub release is published with (default: $LETSGO_RELEASE_TOKEN, else --token)" //nolint:gosec // usage text, not a credential

// splitClientFor returns a client for resolved, or the release client itself
// when resolved names the same credential as the release: one client means
// one connection pool and one user agent, and it keeps the single-credential
// arrangement exactly as it was.
func (f forge) splitClientFor(ctx context.Context, client *github.Client, resolved, token string) *github.Client {
	current, _ := plan.Token(ctx, machineConfig(), token)
	if resolved == current {
		return client
	}
	return f.client(resolved)
}

// tapClientFor returns the client the tap is written with.
func (f forge) tapClientFor(ctx context.Context, client *github.Client, tapToken, token string) *github.Client {
	value, _ := plan.TapToken(ctx, machineConfig(), tapToken, token)
	return f.splitClientFor(ctx, client, value, token)
}

// releaseClientFor returns the client the GitHub release itself is created
// and published with. Mirrors tapClientFor exactly, one split credential at
// a time.
func (f forge) releaseClientFor(ctx context.Context, client *github.Client, releaseToken, token string) *github.Client {
	value, _ := plan.ReleaseToken(ctx, machineConfig(), releaseToken, token)
	return f.splitClientFor(ctx, client, value, token)
}

// applyDraftFlag folds `release --draft` into the plan, so the tap and image
// publishers, which read the plan, hold back for it the same way they do for
// `draft = true` in the config.
func applyDraftFlag(p *plan.Plan, draft bool) {
	if draft {
		p.Config.Draft = true
	}
}

// describeRepo reads the description and licence the formula should carry.
//
// Failure is not fatal: `desc` and `license` are optional in a formula, and a
// release should not stop because a metadata endpoint did. Returning nil means
// the formula is rendered without them.
func describeRepo(ctx context.Context, client *github.Client, repo github.Repo) *github.RepoInfo {
	info, err := client.Repository(ctx, repo)
	if err != nil {
		fmt.Printf("  ! could not read %s's description for the formula: %v\n", repo, err)
		return nil
	}
	return info
}
