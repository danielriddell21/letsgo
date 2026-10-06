package main

import (
	"context"
	"os"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/releaser"
)

// planDiff builds the release into a scratch directory and reads the forge to
// say what releasing would change.
func (f forge) planDiff(ctx context.Context, p *plan.Plan, set credential.Set) (*apply.Diff, error) {
	return releaser.Diff(ctx, p, releaser.Options{
		Clients:     f.clients(set),
		Token:       set.Forge.Value,
		ToolVersion: version,
		Log:         os.Stdout,
		Warn:        os.Stderr,
	})
}
