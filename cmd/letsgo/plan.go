package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/plan"
)

// errPlanFailed marks a failure already reported in full by the plan output,
// so main does not print a second, vaguer version of the same thing.
var errPlanFailed = errors.New("plan failed")

func (f forge) runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	explain := fs.Bool("explain", false, "show where each resolved value came from")
	jsonOutput := fs.Bool("json", false, "print the plan as JSON")
	format := fs.String("format", "text", "how a diff is written: text, or md for a job summary (implies --diff)")
	diff := fs.Bool("diff", false, "also build, read the forge, and show what a release would change there")
	planOut := fs.String("out", "", "save the plan to `file` for `letsgo apply` (implies --diff)")
	exitCode := fs.Bool("exit-code", false, "with --diff, exit 2 when the plan has changes, for drift detection")
	allowVulnerable := fs.Bool("allow-vulnerable", false, "plan to publish despite reachable vulnerabilities, recording which were accepted")
	allowBreaking := fs.Bool("allow-breaking", false, "plan to publish an incompatible API change without a major version bump")
	snapshot := fs.Bool("snapshot", false, "plan an untagged working version")
	allowDirty := fs.Bool("allow-dirty", false, "permit an unclean worktree")
	publishGates := fs.Bool("publish", false, "also check the gates a release needs: a forge, and a token that may write to it")
	analyse := fs.Bool("analyse", false, "also run the slower analysis gates, as a release does")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	tapToken := fs.String("tap-token", "", tapTokenUsage)
	releaseToken := fs.String("release-token", "", releaseTokenUsage)
	yankTag := fs.String("yank", "", "plan retracting the release `tag` instead of publishing one")
	var yankFlags yankArgs
	yankFlags.bind(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	markdown, err := markdownFormat(*format, *jsonOutput)
	if err != nil {
		return err
	}
	run := diffRun{Out: *planOut, ExitCode: *exitCode, Started: time.Now()}
	defer run.toMarkdown(markdown)()

	if *yankTag != "" {
		return f.planYankCommand(*yankTag, yankFlags, *jsonOutput, diffTokens{Token: *token, TapToken: *tapToken, ReleaseToken: *releaseToken}, run)
	}

	saving := *planOut != ""
	diffing := *diff || saving || markdown
	if diffing && *jsonOutput {
		return errors.New("letsgo: --diff and -out have no JSON form yet")
	}
	if *exitCode && !diffing {
		return errors.New("letsgo: --exit-code needs --diff")
	}

	ctx := context.Background()
	started := time.Now()
	p, err := plan.Resolve(ctx, plan.Options{
		Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty,
		Publish: *publishGates, Token: *token, TapToken: *tapToken, ReleaseToken: *releaseToken,
		// A diff predicts a release, whose manifest records the gates it ran.
		Analyse: *analyse || diffing, AllowVulnerable: *allowVulnerable, AllowBreaking: *allowBreaking,
	})
	if err != nil {
		return err
	}

	if *jsonOutput {
		return printPlanJSON(p)
	}

	p.Report(os.Stdout, *explain)

	elapsed := took(started)
	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", elapsed)
		return errPlanFailed
	}
	if diffing {
		run.Tokens = diffTokens{Token: *token, TapToken: *tapToken, ReleaseToken: *releaseToken}
		run.Started = started
		return f.diffAndSave(ctx, p, run)
	}
	fmt.Printf("\n  plan ok in %s · run `letsgo build` to produce artifacts\n", elapsed)
	return nil
}

// printPlanJSON is `letsgo plan --json`: the plan, and a failure if it is not OK.
func printPlanJSON(p *plan.Plan) error {
	data, err := p.JSON()
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	if !p.OK() {
		return errPlanFailed
	}
	return nil
}
