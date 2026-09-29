package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/audit"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// runAudit re-checks a shipped release against today's vulnerability
// database and records the result on the release: see docs/hld/audit.md.
// Scanning every supported major with no tag given, and scheduling, are
// later phases.
func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to audit as owner/name (default: this repository's origin)")
	work := fs.String("work", "", "scratch directory (default: a temporary one)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errUsage("letsgo audit <tag>")
	}

	ctx := context.Background()
	started := time.Now()

	repo, _, err := targetRepo(ctx, *repoFlag)
	if err != nil {
		return err
	}

	workDir := *work
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "letsgo-audit-")
		if err != nil {
			return fmt.Errorf("letsgo: scratch directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(workDir) }()
	}

	tokenValue, _ := plan.Token(*token)
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	result, err := audit.Run(ctx, audit.Options{
		Client: client, Repo: repo, Tag: fs.Arg(0), WorkDir: workDir,
	})
	if err != nil {
		return err
	}

	result.Report(os.Stdout)
	if result.Recorded {
		fmt.Println("\n  recorded in audit.json")
	} else {
		fmt.Println("\n  no change since the last audit; not recorded")
	}
	fmt.Printf("  audited in %s\n", took(started))
	return nil
}
