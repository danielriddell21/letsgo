package main

import (
	"context"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/releaser"
)

// tapTokenUsage documents --tap-token once, for the three commands that reach
// a tap.
const tapTokenUsage = "token the Homebrew tap is written with (default: $LETSGO_TAP_TOKEN, else the release token)"

// releaseTokenUsage documents --release-token once.
const releaseTokenUsage = "token the GitHub release is published with (default: $LETSGO_RELEASE_TOKEN, else --token)" //nolint:gosec // usage text, not a credential

// credentials resolves the credentials a command was given, once, from its
// flags, the environment and the global config's token-command. Everything
// below the command takes the result rather than looking tokens up itself.
func (f forge) credentials(ctx context.Context, flags credential.Flags) credential.Set {
	return credential.Resolve(ctx, f.machine(), flags)
}

// forgeToken is the token of a command that reaches a forge but writes only
// with that one credential.
func (f forge) forgeToken(ctx context.Context, flag string) string {
	return f.credentials(ctx, credential.Flags{Token: flag}).Forge.Value
}

// splitClient returns the client c is written with: client itself when c names
// the same credential as the forge's, since one client means one connection
// pool and one user agent and keeps the single-credential arrangement exactly
// as it was; a client of its own otherwise.
func (f forge) splitClient(client *github.Client, forgeCred, c credential.Credential) *github.Client {
	if c.Value == forgeCred.Value {
		return client
	}
	return f.client(c.Value)
}

// tapClient is the client the Homebrew tap is written with.
func (f forge) tapClient(client *github.Client, set credential.Set) *github.Client {
	return f.splitClient(client, set.Forge, set.Tap)
}

// clients are the three clients a release is written through.
//
// The tap gets its own client, so that the credential which can write to
// another repository need not be one that can also write to this one. They
// are the same client when no tap token is configured, which is what makes the
// split opt-in rather than a migration. The release itself is split the same
// way, so it can be published under the same bot identity as the tap commit
// instead of whatever token ran the workflow.
func (f forge) clients(set credential.Set) releaser.Clients {
	client := f.client(set.Forge.Value)
	return releaser.Clients{
		Read:    client,
		Release: f.splitClient(client, set.Forge, set.Release),
		Tap:     f.tapClient(client, set),
	}
}
