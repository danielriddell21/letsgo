package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/releaser"
	"github.com/danielriddell21/letsgo/manifest"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	snapshot := fs.Bool("snapshot", false, "build an untagged working version")
	allowDirty := fs.Bool("allow-dirty", false, "permit an unclean worktree")
	out := fs.String("o", "dist", "output directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	res, err := releaser.Build(context.Background(), releaser.Options{
		Plan:        plan.Options{Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty},
		ToolVersion: version,
		Out:         *out,
		Log:         os.Stdout,
	})
	if err != nil {
		return err
	}
	result, dir := res.Build, res.Dir

	fmt.Printf("\n  built %d files in %s\n", len(result.Files), res.Took)
	for _, a := range result.Artifacts {
		fmt.Printf("    %s  %s\n", a.ArchiveSHA256[:12], a.Archive)
	}
	fmt.Printf("    %s  %s\n", result.Source.SHA256[:12], result.Source.Name)
	fmt.Printf("    %-12s  %s\n", "", manifest.FileName)
	fmt.Printf("    %-12s  %s\n", "", build.ChecksumFile)

	// The image is assembled, not pushed. Its digest is final either way, so
	// there is something specific to print rather than a promise.
	if len(result.Images) > 0 {
		fmt.Printf("\n  images assembled, not pushed\n")
		fmt.Print(release.Describe(result.Images))
	}

	fmt.Printf("\n  %s\n", dir)
	return nil
}

// releaseArgs is everything `letsgo release` and `letsgo apply` take on the
// command line.
type releaseArgs struct {
	draft, skipWarm, snapshot, appendNotes, allowVulnerable, allowBreaking bool
	token, tapToken, releaseToken, out                                     string
}

// bindCredentials declares the flags every release-shaped command shares.
func (a *releaseArgs) bindCredentials(fs *flag.FlagSet) {
	fs.StringVar(&a.token, "token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	fs.StringVar(&a.tapToken, "tap-token", "", tapTokenUsage)
	fs.StringVar(&a.releaseToken, "release-token", "", releaseTokenUsage)
	fs.StringVar(&a.out, "o", "dist", "output directory")
	fs.BoolVar(&a.skipWarm, "no-proxy-warm", false, "skip priming the Go module proxy")
	fs.BoolVar(&a.allowVulnerable, "allow-vulnerable", false, "publish despite reachable vulnerabilities, recording which were accepted")
	fs.BoolVar(&a.allowBreaking, "allow-breaking", false, "publish an incompatible API change without a major version bump")
}

func (f forge) runRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	var a releaseArgs
	a.bindCredentials(fs)
	fs.BoolVar(&a.draft, "draft", false, "create the release without publishing it")
	fs.BoolVar(&a.snapshot, "snapshot", false, "rehearse the release without publishing anything")
	fs.BoolVar(&a.appendNotes, "append-notes", false, "add the changelog after an existing release description instead of replacing it")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return f.doRelease(context.Background(), a, nil)
}

// runApply publishes a release exactly as a saved plan agreed it: the same
// commit, rebuilt to the same manifest, or nothing at all.
//
// With no file it makes the plan itself, shows it, and asks before applying
// it, so a local apply is as considered as one from a plan made earlier.
func (f forge) runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	var a releaseArgs
	a.bindCredentials(fs)
	autoApprove := fs.Bool("auto-approve", false, "with no plan file, apply the plan without asking first")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errUsage("letsgo apply [plan file] [-auto-approve]")
	}

	if fs.NArg() == 0 {
		return f.applyFresh(context.Background(), a, *autoApprove)
	}
	return f.applyPlanFile(a, fs.Arg(0))
}

// applyPlanFile applies the plan saved at path.
func (f forge) applyPlanFile(a releaseArgs, path string) error {
	file, err := plandiff.Read(path)
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	digest, err := file.Digest()
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	fmt.Printf("  applying %s (%s) for %s\n", path, apply.Short12(strings.TrimPrefix(digest, "sha256:")), file.Tag)

	if file.Kind == plandiff.FileKindYank {
		return f.applyYank(context.Background(), file, diffTokens{Token: a.token, TapToken: a.tapToken, ReleaseToken: a.releaseToken})
	}
	return f.doRelease(context.Background(), a, file)
}

// stdinIsTerminal reports whether there is someone to ask. It is a variable so
// a test can be that someone.
var stdinIsTerminal = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// applyFresh plans a release, shows the plan, and applies exactly that plan
// once it is agreed.
//
// The agreement is either a yes at the prompt or -auto-approve. With neither
// possible the command stops before it has built anything, because a question
// nobody can answer is not consent.
func (f forge) applyFresh(ctx context.Context, a releaseArgs, autoApprove bool) error {
	if !autoApprove && !stdinIsTerminal() {
		return errors.New("letsgo: apply with no plan file asks before it publishes, and there is no terminal to ask on; pass -auto-approve, or save a plan with `letsgo plan -out` and apply that")
	}

	dir, err := os.MkdirTemp("", "letsgo-apply-")
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "release.plan")

	changed, err := f.planForApply(ctx, a, path)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if !autoApprove {
		fmt.Print("\n  Apply? [y/N] ")
		if !readYes() {
			fmt.Println("\n  nothing was published")
			return nil
		}
	}
	fmt.Println()
	return f.applyPlanFile(a, path)
}

// planForApply resolves and diffs a release as `letsgo plan -out` does, shows
// it, and saves it at path. It reports whether there is anything to apply.
func (f forge) planForApply(ctx context.Context, a releaseArgs, path string) (bool, error) {
	started := time.Now()
	tokens := diffTokens{Token: a.token, TapToken: a.tapToken, ReleaseToken: a.releaseToken}
	p, err := plan.Resolve(ctx, plan.Options{
		Dir: ".", Publish: true, Token: a.token, TapToken: a.tapToken, ReleaseToken: a.releaseToken,
		Analyse: true, AllowVulnerable: a.allowVulnerable, AllowBreaking: a.allowBreaking,
		NewClient: f.client, DisableProxyWarm: a.skipWarm,
	})
	if err != nil {
		return false, err
	}
	p.Report(os.Stdout, false)
	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", took(started))
		return false, errPlanFailed
	}

	d, err := f.planDiff(ctx, p, tokens)
	if err != nil {
		return false, err
	}
	if err := finishPlan(d.Actions, nil, diffRun{Started: started}); err != nil {
		return false, err
	}
	if !plandiff.HasChanges(d.Actions) {
		return false, nil
	}
	if _, err := apply.Save(p, d, path, version); err != nil {
		return false, err
	}
	return true, nil
}

// doRelease builds and publishes a release. applied, when set, is the plan the
// release must keep to: it is held to before anything is published, and only
// what it lists is written.
func (f forge) doRelease(ctx context.Context, a releaseArgs, applied *plandiff.File) error {
	tokens := diffTokens{Token: a.token, TapToken: a.tapToken, ReleaseToken: a.releaseToken}
	clients, tokenValue := f.clients(ctx, tokens)

	res, err := releaser.Release(ctx, releaser.Options{
		Plan: plan.Options{
			Dir: ".", Publish: !a.snapshot, Token: a.token, TapToken: a.tapToken, ReleaseToken: a.releaseToken,
			Snapshot: a.snapshot, Analyse: true, AllowVulnerable: a.allowVulnerable, AllowBreaking: a.allowBreaking,
			DisableProxyWarm: a.skipWarm, NewClient: f.client,
		},
		Clients:     clients,
		Token:       tokenValue,
		ToolVersion: version,
		Out:         a.out,
		Log:         os.Stdout,
		Warn:        os.Stderr,
		Applied:     applied,
		Draft:       a.draft,
		Snapshot:    a.snapshot,
		AppendNotes: a.appendNotes,
	})
	if err != nil {
		return err
	}

	if res.Snapshot {
		fmt.Printf("\n  rehearsed in %s \u00b7 nothing was published\n  artifacts: %s\n", res.Took, res.Dir)
		return nil
	}
	fmt.Printf("\n  released in %s\n  %s\n", res.Took, res.URL)
	return nil
}
