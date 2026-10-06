package main

import (
	"context"
	"os"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/releaser"
)

// diffTokens are the credentials planDiff reads the forge with.
type diffTokens struct {
	Token, TapToken, ReleaseToken string
}

// planDiff builds the release into a scratch directory and reads the forge to
// say what releasing would change.
func (f forge) planDiff(ctx context.Context, p *plan.Plan, tokens diffTokens) (*apply.Diff, error) {
	clients, tokenValue := f.clients(ctx, tokens)
	return releaser.Diff(ctx, p, releaser.Options{
		Clients:     clients,
		Token:       tokenValue,
		ToolVersion: version,
		Log:         os.Stdout,
		Warn:        os.Stderr,
	})
}
