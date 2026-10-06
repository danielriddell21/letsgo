package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/audit"
)

// runAudit re-checks a shipped release against today's vulnerability
// database and records the result on the release: see docs/hld/audit.md.
// With a tag, it audits that one release; with none, it audits the newest
// stable release of every major version in this module's scope.
func (f forge) runAudit(args []string) error {
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

	run, err := f.resolveScratchRun(ctx, scratchOptions{Repo: *repoFlag, Token: *token, Work: *work, TmpPrefix: "letsgo-audit-"})
	if err != nil {
		return err
	}
	defer run.cleanup()

	opts := audit.Options{Global: f.machine(), Client: run.Client, Repo: run.Repo, Prefix: run.Prefix, WorkDir: run.WorkDir}

	if fs.NArg() == 1 {
		result, err := audit.Run(ctx, audit.Options{
			Global: f.machine(), Client: run.Client, Repo: run.Repo, Tag: fs.Arg(0), WorkDir: run.WorkDir,
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
	if result.Skipped != "" {
		return
	}
	if result.Recorded {
		fmt.Println("  recorded in audit.json")
	} else {
		fmt.Println("  no change since the last audit; not recorded")
	}
}
