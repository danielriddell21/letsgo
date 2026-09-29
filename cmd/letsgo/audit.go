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
// With a tag, it audits that one release; with none, it audits the newest
// stable release of every major version in this module's scope.
func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to audit as owner/name (default: this repository's origin)")
	work := fs.String("work", "", "scratch directory (default: a temporary one)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errUsage("letsgo audit [<tag>]")
	}

	ctx := context.Background()
	started := time.Now()

	repo, dir, err := targetRepo(ctx, *repoFlag)
	if err != nil {
		return err
	}
	prefix, err := scopePrefix(ctx, *repoFlag, dir)
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

	opts := audit.Options{Client: client, Repo: repo, Prefix: prefix, WorkDir: workDir}

	if fs.NArg() == 1 {
		result, err := audit.Run(ctx, audit.Options{
			Client: client, Repo: repo, Tag: fs.Arg(0), WorkDir: workDir,
		})
		if err != nil {
			return err
		}
		reportAuditResult(result)
		fmt.Printf("  audited in %s\n", took(started))
		return nil
	}

	results, err := audit.RunAll(ctx, opts)
	if err != nil {
		return err
	}
	for _, result := range results {
		reportAuditResult(result)
	}
	fmt.Printf("  audited %d release(s) in %s\n", len(results), took(started))
	return nil
}

func reportAuditResult(result *audit.Result) {
	result.Report(os.Stdout)
	if result.Recorded {
		fmt.Println("  recorded in audit.json")
	} else {
		fmt.Println("  no change since the last audit; not recorded")
	}
}
