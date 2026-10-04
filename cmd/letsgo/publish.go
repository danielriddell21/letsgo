package main

import (
	"context"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/releaser"
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

// clients are the three clients a release is written through, built from the
// credentials the commands were given, and the token value the container
// registry is written with. Resolving them is the caller's job, not the
// releaser's: it never reads the environment or the machine config.
func (f forge) clients(ctx context.Context, tokens diffTokens) (releaser.Clients, string) {
	value, _ := plan.Token(ctx, machineConfig(), tokens.Token)
	client := f.client(value)

	// The tap gets its own client, so that the credential which can write to
	// another repository need not be one that can also write to this one. They
	// are the same client when no tap token is configured, which is what makes
	// the split opt-in rather than a migration. The release itself is split
	// the same way, so it can be published under the same bot identity as the
	// tap commit instead of whatever token ran the workflow.
	return releaser.Clients{
		Read:    client,
		Release: f.releaseClientFor(ctx, client, tokens.ReleaseToken, tokens.Token),
		Tap:     f.tapClientFor(ctx, client, tokens.TapToken, tokens.Token),
	}, value
}
