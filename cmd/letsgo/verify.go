package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/receipt"
	"github.com/danielriddell21/letsgo/internal/verify"
)

// errVerifyFailed likewise: the report already names every mismatch.
var errVerifyFailed = errors.New("verification failed")

func (f forge) runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to verify as owner/name (default: this repository's origin)")
	noRebuild := fs.Bool("no-rebuild", false, "compare published assets against the manifest without rebuilding")
	work := fs.String("work", "", "scratch directory (default: a temporary one)")
	jsonOutput := fs.Bool("json", false, "print the report as JSON")
	words := fs.Bool("words", false, "also print the manifest digest as words, for reading aloud")
	receipted := fs.Bool("receipt", false, "")
	hideFromUsage(fs, "receipt")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	run, err := f.resolveScratchRun(ctx, *repoFlag, *token, *work, "letsgo-verify-")
	if err != nil {
		return err
	}
	defer run.cleanup()

	goBin, _, _ := gobuild.Toolchain(machineConfig())
	result, err := verify.Run(ctx, verify.Options{
		Client: run.Client, Repo: run.Repo, Tag: fs.Arg(0), Prefix: run.Prefix,
		Dir: run.Dir, WorkDir: run.WorkDir, SkipRebuild: *noRebuild,
		UserAgent: "letsgo/" + version,
		GoBin:     goBin,
		GitBin:    run.GitBin,
	})
	if err != nil {
		return err
	}

	switch {
	case *jsonOutput:
		data, err := result.JSON()
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	case *receipted:
		fmt.Print(receipt.Render(result, time.Now()))
	default:
		result.Report(os.Stdout)
		if *words {
			result.ReportWords(os.Stdout)
		}
	}

	quiet := *jsonOutput || *receipted
	if !result.OK() {
		if !quiet {
			fmt.Printf("\n  %s does not verify (%s)\n", result.Tag, took(started))
		}
		return errVerifyFailed
	}
	if !quiet {
		fmt.Printf("\n  %s verified in %s\n", result.Tag, took(started))
	}
	return nil
}
