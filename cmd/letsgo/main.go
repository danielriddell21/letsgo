// Command letsgo builds and publishes Go releases.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/pgpwords"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/randomart"
	"github.com/danielriddell21/letsgo/internal/receipt"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/verify"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// version is replaced at link time. It is declared exactly the way letsgo
// expects its users to declare it, so the tool releases itself the same way it
// releases anything else.
var version = "dev"

const usage = `letsgo builds and publishes Go releases.

usage:
  letsgo plan [--explain] [--json] [--publish] [--diff [--exit-code]] [-out file]  resolve and check a release without performing one
  letsgo build [--snapshot] [-o dir]     build every artifact into dist/ without publishing
  letsgo release [--draft] [-o dir]      build and publish, resumably
  letsgo apply <file> [-o dir]          publish a release exactly as a saved plan agreed it
  letsgo release --snapshot              rehearse a release without publishing
  letsgo verify [tag] [--json] [--words]  rebuild a published release and compare it
  letsgo doctor [--json]                 diagnose tools and repository state, read-only
  letsgo audit [<tag>]                   re-check published releases against today's vulnerability database
  letsgo diff <from> [to] [--format text|md|json]  compare two releases: size, dependencies, API
  letsgo promote <rc-tag>                rebuild a prerelease as a stable release
  letsgo yank <tag> [--reason "..."]     retract a release, including the go.mod directive
  letsgo tag [--major|--minor|--patch|--pre|--json]  work out the next version and tag it
  letsgo update [--check]                update letsgo itself, verified against its manifest
  letsgo plugin install <name>           install a plugin, verified against its manifest
  letsgo plugin list [--json]            the plugins this repository pins, and what is installed
  letsgo features [--json]               the feature catalogue: what can be disabled or required
  letsgo fmt [file|-]                    format letsgo.mod; - reads stdin, writes to stdout
  letsgo lsp [--restricted]              serve letsgo.mod over stdio JSON-RPC, for an editor
  letsgo version                         print the version (also --version)

run a command with -h for its options.
`

// commands is the whole surface of the tool: one verb to the function that
// runs it.
//
// A table rather than a switch, so that adding a verb is an entry here and not
// a change to the program's entry point. It is also why the aliases sit beside
// the names they alias instead of sharing a case.
var commands = map[string]func([]string) error{
	"plan":     runPlan,
	"build":    runBuild,
	"release":  runRelease,
	"apply":    runApply,
	"verify":   runVerify,
	"doctor":   runDoctor,
	"audit":    runAudit,
	"diff":     runDiff,
	"promote":  runPromote,
	"yank":     runYank,
	"update":   runUpdate,
	"plugin":   runPlugin,
	"tag":      runTag,
	"fmt":      runFmt,
	"features": runFeatures,
	"lsp":      runLSP,

	"version":   runVersion,
	"--version": runVersion,
	"-version":  runVersion,
	"-v":        runVersion,

	"help":   runHelp,
	"-h":     runHelp,
	"--help": runHelp,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	command, args := os.Args[1], os.Args[2:]

	run, ok := commands[command]
	if !ok {
		fmt.Fprintf(os.Stderr, "letsgo: unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}

	if err := run(args); err != nil {
		if code := exitCode(err); code != 1 {
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "letsgo:", err)
		os.Exit(1)
	}
}

// exitCode is the status a failed command exits with. A plan that has changes
// is not a failure, and is told apart from one so that a script can act on
// drift without treating it as an error.
func exitCode(err error) int {
	if errors.Is(err, errPlanChanges) {
		return 2
	}
	return 1
}

func runVersion([]string) error {
	fmt.Println("letsgo", version)
	return nil
}

func runHelp([]string) error {
	fmt.Print(usage)
	return nil
}

// parseFlags parses a command's flags.
//
// A wrapper rather than a bare call at each site: every subcommand does this,
// and an error out of the flag package reaching the user unprefixed would not
// say which tool produced it.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(permute(fs, args)); err != nil {
		return fmt.Errorf("letsgo %s: %w", fs.Name(), err)
	}
	return nil
}

// permute moves flags ahead of positional arguments.
//
// Go's flag package stops at the first non-flag argument, so `letsgo yank
// v1.2.3 --reason "..."` would treat the flag as another operand. Every one of
// these commands documents its operand first, and typing it that way should
// not silently mean something else.
func permute(fs *flag.FlagSet, args []string) []string {
	var flags, operands []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		// Everything after "--" is an operand by definition, and stays in
		// order behind the terminator.
		case arg == "--":
			operands = append(operands, args[i+1:]...)
			return append(flags, append([]string{"--"}, operands...)...)

		case len(arg) > 1 && arg[0] == '-':
			flags = append(flags, arg)

			name := strings.TrimLeft(arg, "-")
			if strings.Contains(name, "=") {
				continue // the value is attached
			}
			// A non-boolean flag takes the next argument with it, or the
			// reordering would separate a flag from its value.
			if f := fs.Lookup(name); f != nil && !boolFlag(f) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}

		default:
			operands = append(operands, arg)
		}
	}
	return append(flags, operands...)
}

func boolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// errPlanFailed marks a failure already reported in full by the plan output,
// so main does not print a second, vaguer version of the same thing.
var errPlanFailed = errors.New("plan failed")

// errVerifyFailed likewise: the report already names every mismatch.
var errVerifyFailed = errors.New("verification failed")

// errDoctorFailed likewise: the report already names every failing check.
var errDoctorFailed = errors.New("doctor found a problem")

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	explain := fs.Bool("explain", false, "show where each resolved value came from")
	jsonOutput := fs.Bool("json", false, "print the plan as JSON")
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
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	saving := *planOut != ""
	diffing := *diff || saving
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

	p.Report(os.Stdout, *explain)

	elapsed := took(started)
	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", elapsed)
		return errPlanFailed
	}
	if diffing {
		return diffAndSave(ctx, p, diffRun{
			Tokens: diffTokens{Token: *token, TapToken: *tapToken, ReleaseToken: *releaseToken},
			Out:    *planOut, ExitCode: *exitCode, Started: started,
		})
	}
	fmt.Printf("\n  plan ok in %s · run `letsgo build` to produce artifacts\n", elapsed)
	return nil
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	snapshot := fs.Bool("snapshot", false, "build an untagged working version")
	allowDirty := fs.Bool("allow-dirty", false, "permit an unclean worktree")
	out := fs.String("o", "dist", "output directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	_, dir, result, _, err := planAndBuild(ctx, planBuildOptions{
		Out:         *out,
		Plan:        plan.Options{Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty},
		FailureNote: "nothing was built",
		Started:     started,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n  built %d files in %s\n", len(result.Files), took(started))
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

func runRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	var a releaseArgs
	a.bindCredentials(fs)
	fs.BoolVar(&a.draft, "draft", false, "create the release without publishing it")
	fs.BoolVar(&a.snapshot, "snapshot", false, "rehearse the release without publishing anything")
	fs.BoolVar(&a.appendNotes, "append-notes", false, "add the changelog after an existing release description instead of replacing it")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return doRelease(context.Background(), a, nil)
}

// runApply publishes a release exactly as a saved plan agreed it: the same
// commit, rebuilt to the same manifest, or nothing at all.
func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	var a releaseArgs
	a.bindCredentials(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("letsgo: apply needs a plan file; make one with `letsgo plan -out`")
	}

	file, err := plandiff.Read(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	digest, err := file.Digest()
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	fmt.Printf("  applying %s (%s) for %s\n", fs.Arg(0), short12(strings.TrimPrefix(digest, "sha256:")), file.Tag)

	return doRelease(context.Background(), a, file)
}

// doRelease builds and publishes a release. applied, when set, is the plan the
// release must keep to: it is held to before anything is published, and only
// what it lists is written.
func doRelease(ctx context.Context, a releaseArgs, applied *plandiff.File) error {
	started := time.Now()

	tokenValue, _ := plan.Token(a.token)
	client := newForgeClient(tokenValue)

	// A rehearsal needs no forge and no token, so the gates that check for
	// them are not run. The repository's description and licence are read
	// regardless — a tap-files plugin's cask needs them exactly as a formula
	// does, and a rehearsal has to reach every decision a real run reaches.
	p, dir, result, info, err := planAndBuild(ctx, planBuildOptions{
		Out: a.out,
		Plan: plan.Options{
			Dir: ".", Publish: !a.snapshot, Token: a.token, TapToken: a.tapToken, ReleaseToken: a.releaseToken,
			Snapshot: a.snapshot, Analyse: true, AllowVulnerable: a.allowVulnerable, AllowBreaking: a.allowBreaking,
			DisableProxyWarm: a.skipWarm,
		},
		FailureNote: "nothing was built or published",
		Started:     started,
		Describe: func(p *plan.Plan) *github.RepoInfo {
			if !wantsRepoInfo(p) {
				return nil
			}
			return describeRepo(ctx, client, github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name})
		},
	})
	if err != nil {
		return err
	}
	fmt.Printf("\n  built %d files\n", len(result.Files))

	if applied, err = holdAndStamp(applied, p, result); err != nil {
		return err
	}

	// The tap gets its own client, so that the credential which can write to
	// another repository need not be one that can also write to this one. They
	// are the same client when no tap token is configured, which is what makes
	// the split opt-in rather than a migration.
	tapClient := tapClientFor(client, a.tapToken, a.token)

	// The release itself gets its own client the same way, so it can be
	// published under the same bot identity as the tap commit instead of
	// whatever token ran the workflow.
	releaseClient := releaseClientFor(client, a.releaseToken, a.token)

	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}

	manifestSum, err := fileSum(filepath.Join(result.Dir, manifest.FileName))
	if err != nil {
		return err
	}
	notes, err := releaseNotes(ctx, p, client, repo, result.Manifest, manifestSum)
	if err != nil {
		return err
	}

	// Everything above this line is identical in a rehearsal. Only the thing
	// that writes to the world is exchanged.
	forge, tapAPI, err := guardApply(ctx, p, applied, forgeTargets{
		Forge: releaseClient, Tap: tapClient, Token: tokenValue, Repo: repo, Dir: dir,
		Result: result, Notes: notes, Info: info,
	})
	if err != nil {
		return err
	}
	if a.snapshot {
		fmt.Println("\n  rehearsal: the calls below would be made, and are not")
		recorder := publish.NewRecorder(os.Stdout)
		forge, tapAPI = recorder, recorder
	}

	// Before anything is attached: a disagreement with sum.golang.org must
	// stop the release, not annotate one that is already public.
	if err := warmProxyAndCheckSumdb(ctx, p, dir, result, a.snapshot, a.draft || p.Config.Draft); err != nil {
		return err
	}

	published, err := publish.Run(ctx, releaseOptions(p, releaseRun{
		Forge: forge, Repo: repo, Dir: dir, Result: result, Notes: notes,
		Draft: a.draft || p.Config.Draft, Append: a.appendNotes,
		Logf: func(format string, args ...any) {
			fmt.Printf("  "+format+"\n", args...)
		},
	}))
	if err != nil {
		return err
	}
	reportPublished(published)

	// After publication, because a formula names download URLs that only
	// exist once the assets are attached.
	if err := publishTap(ctx, p, result, tapAPI, repo, info); err != nil {
		return err
	}

	if err := publishImages(ctx, p, result, tokenValue, a.snapshot); err != nil {
		return err
	}

	if a.snapshot {
		fmt.Printf("\n  rehearsed in %s \u00b7 nothing was published\n  artifacts: %s\n", took(started), dir)
		return nil
	}

	fmt.Printf("\n  released in %s\n  %s\n", took(started), published.Release.HTMLURL)
	return nil
}

// releaseRun is everything publishing a release depends on beyond the plan.
type releaseRun struct {
	Forge  publish.Forge
	Repo   github.Repo
	Dir    string
	Result *release.Result
	Notes  string
	Draft  bool
	Append bool
	Logf   func(format string, args ...any)
}

// releaseOptions is the publication of a release, decided once so that what
// `letsgo release` does and what `letsgo plan --diff` predicts it will do
// cannot drift apart.
func releaseOptions(p *plan.Plan, r releaseRun) publish.Options {
	return publish.Options{
		Client: r.Forge,
		Repo:   r.Repo,
		Dir:    r.Dir,
		Files:  r.Result.Files,
		Sums:   sumsFrom(r.Result),
		Notes:  notesMode(r.Append, p.Features.On("changelog")),
		Release: github.ReleaseInput{
			TagName:         releaseTag(p),
			Name:            releaseTitle(p),
			Body:            r.Notes,
			Draft:           r.Draft,
			Prerelease:      isPrerelease(p),
			MakeLatest:      isLatest(p),
			TargetCommitish: p.Git.Commit,
		},
		Logf: r.Logf,
	}
}

// planBuildOptions describes the resolve-report-build sequence both `letsgo
// build` and `letsgo release` open with.
type planBuildOptions struct {
	Plan plan.Options
	Out  string

	// FailureNote says what did not happen when the plan fails, which differs
	// between building and releasing.
	FailureNote string

	Started time.Time

	// Describe reads the repository's description, licence and homepage once
	// the plan is known to have one, for a tap-files plugin's cask. Nil skips
	// it \u2014 `letsgo build` never touches the network.
	Describe func(p *plan.Plan) *github.RepoInfo
}

// planAndBuild resolves a plan, prints its report, and builds it.
//
// Shared so that what `letsgo build` produces locally is what `letsgo release`
// uploads, decided by the same code rather than by two sequences that agree
// today.
func planAndBuild(ctx context.Context, o planBuildOptions) (*plan.Plan, string, *release.Result, *github.RepoInfo, error) {
	p, err := plan.Resolve(ctx, o.Plan)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("letsgo: %w", err)
	}
	p.Report(os.Stdout, false)

	if !p.OK() {
		fmt.Printf("\n  plan failed in %s \u00b7 %s\n", took(o.Started), o.FailureNote)
		return nil, "", nil, nil, errPlanFailed
	}

	dir, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("letsgo: %w", err)
	}

	info := repoInfoFor(o, p)

	result, err := release.Build(ctx, p, dir, version, info, func(format string, args ...any) {
		fmt.Printf("    ! "+format+"\n", args...)
	})
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("letsgo: %w", err)
	}
	return p, dir, result, info, nil
}

// repoInfoFor runs o.Describe when the caller set one, nil otherwise —
// `letsgo build` never does.
func repoInfoFor(o planBuildOptions, p *plan.Plan) *github.RepoInfo {
	if o.Describe == nil {
		return nil
	}
	return o.Describe(p)
}

// wantsRepoInfo reports whether a release should read the repository's
// description before building: only when there is a Homebrew tap to write a
// formula (or a tap-files plugin's cask) into, so a release with none never
// touches the endpoint.
func wantsRepoInfo(p *plan.Plan) bool {
	return p.Tap != (github.Repo{}) && p.HasRepo
}

// reportPublished summarises what reached the forge.
func reportPublished(published *publish.Result) {
	if published.NotesRefused {
		fmt.Println("  ! the release description could not be updated with this token")
	}

	fmt.Printf("  uploaded %d, skipped %d", len(published.Uploaded), len(published.Skipped))
	if len(published.Replaced) > 0 {
		fmt.Printf(", replaced %d", len(published.Replaced))
	}
	fmt.Println()
}

// warmProxy primes the resolved module proxy so `go install` works
// immediately.
//
// Best effort, and deliberately after publication: a proxy that is slow has
// not broken a release that is already live.
func warmProxy(ctx context.Context, p *plan.Plan) {
	if err := publish.WarmProxy(ctx, p.Proxy, p.Module.Path, p.Version); err != nil {
		fmt.Printf("  ! could not prime the module proxy: %v\n", err)
		fmt.Printf("    `go install` may fail briefly until the proxy fetches %s\n", p.Tag)
		return
	}
	fmt.Printf("  primed %s\n", p.Proxy)
}

// warmProxyAndCheckSumdb primes the module proxy, then cross-checks
// sum.golang.org and the proxy against the built source archive — on every
// non-snapshot, non-draft, non-scoped release, before any asset is attached.
// The tag is already pushed, which is all the proxy needs.
func warmProxyAndCheckSumdb(ctx context.Context, p *plan.Plan, dir string, result *release.Result, snapshot, draft bool) error {
	if snapshot || draft || p.Config.ModuleDir != "" {
		return nil
	}
	if p.Features.On("proxy-warm") {
		warmProxy(ctx, p)
	}
	return checkSumdb(ctx, p, dir, result)
}

func runVerify(args []string) error {
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

	run, err := resolveScratchRun(ctx, *repoFlag, *token, *work, "letsgo-verify-")
	if err != nil {
		return err
	}
	defer run.cleanup()

	result, err := verify.Run(ctx, verify.Options{
		Client: run.Client, Repo: run.Repo, Tag: fs.Arg(0), Prefix: run.Prefix,
		Dir: run.Dir, WorkDir: run.WorkDir, SkipRebuild: *noRebuild,
		UserAgent: "letsgo/" + version,
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

// hideFromUsage keeps a flag working but out of the command's --help.
func hideFromUsage(fs *flag.FlagSet, name string) {
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		fs.VisitAll(func(f *flag.Flag) {
			if f.Name == name {
				return
			}
			fmt.Fprintf(fs.Output(), "  -%s\n    \t%s\n", f.Name, f.Usage)
		})
	}
}

// moduleRepo bundles what a command needs to act on this module's own
// release: the checkout, its scope within a monorepo, and an authenticated
// forge client.
type moduleRepo struct {
	Module discover.Module
	Git    discover.Git
	Repo   github.Repo
	Scope  discover.Scope
	Token  string
	Client *github.Client
}

// resolveModuleRepo runs the bootstrapping every command that acts on "the
// current repository's release" (not an arbitrary --repo) needs before it
// can do anything else: find the module, its repository, its git checkout,
// its scope, and a forge client authenticated with token (or the default
// env vars, when token is empty).
func resolveModuleRepo(ctx context.Context, token string) (moduleRepo, error) {
	module, err := discover.FindModule(".")
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	found, err := discover.FindRepo(ctx, module.Dir)
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	repo := github.Repo{Owner: found.Owner, Name: found.Name}

	git, err := discover.FindGit(ctx, module.Dir)
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}
	scope, err := discover.NewScope(git.TopLevel, module.Dir)
	if err != nil {
		return moduleRepo{}, fmt.Errorf("letsgo: %w", err)
	}

	tokenValue, _ := plan.Token(token)
	if tokenValue == "" {
		return moduleRepo{}, fmt.Errorf("letsgo: no token; set %s", envList())
	}
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	return moduleRepo{Module: module, Git: git, Repo: repo, Scope: scope, Token: tokenValue, Client: client}, nil
}

// scratchRun bundles what a command needs to act on a release with an
// optional --repo override: the target repository, its local module
// directory (empty when --repo named a repository outside this checkout),
// its scope prefix, scratch space for extracted source, and a forge client.
type scratchRun struct {
	Repo    github.Repo
	Dir     string
	Prefix  string
	WorkDir string
	Client  *github.Client
	cleanup func()
}

// resolveScratchRun is the bootstrapping runVerify and runAudit share: both
// accept an optional --repo (unlike resolveModuleRepo, which always acts on
// this checkout's own release) and tolerate an anonymous client for a
// public repository (unlike resolveModuleRepo, which requires a token).
// workDir is created under a temporary directory named tmpPrefix when work
// is empty; cleanup removes it, and is a no-op when work was given
// explicitly.
func resolveScratchRun(ctx context.Context, repoFlag, token, work, tmpPrefix string) (scratchRun, error) {
	repo, dir, err := targetRepo(ctx, repoFlag)
	if err != nil {
		return scratchRun{}, err
	}
	prefix, err := scopePrefix(ctx, repoFlag, dir)
	if err != nil {
		return scratchRun{}, err
	}

	workDir := work
	cleanup := func() {}
	if workDir == "" {
		workDir, err = os.MkdirTemp("", tmpPrefix)
		if err != nil {
			return scratchRun{}, fmt.Errorf("letsgo: scratch directory: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(workDir) }
	}

	tokenValue, _ := plan.Token(token)
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	return scratchRun{Repo: repo, Dir: dir, Prefix: prefix, WorkDir: workDir, Client: client, cleanup: cleanup}, nil
}

// targetRepo resolves which repository a command is asking about and, where
// possible, a local checkout of it.
//
// Inspecting someone else's release is the point, so a repository outside the
// current directory is allowed; it simply has no local checkout, and the
// callers that need one say so.
func targetRepo(ctx context.Context, explicit string) (github.Repo, string, error) {
	if explicit != "" {
		owner, name, ok := strings.Cut(explicit, "/")
		if !ok || owner == "" || name == "" {
			return github.Repo{}, "", fmt.Errorf("--repo must be owner/name, got %q", explicit)
		}
		return github.Repo{Owner: owner, Name: name}, "", nil
	}

	module, err := discover.FindModule(".")
	if err != nil {
		return github.Repo{}, "", fmt.Errorf("%w (use --repo to verify a release elsewhere)", err)
	}
	found, err := discover.FindRepo(ctx, module.Dir)
	if err != nil {
		return github.Repo{}, "", err
	}
	return github.Repo{Owner: found.Owner, Name: found.Name}, module.Dir, nil
}

// scopePrefix resolves the module's scope prefix (see discover.Scope), so
// "no tag given" can find the latest release within this module's own
// scope rather than the repository's overall latest — the same distinction
// runYank draws before picking a previous release.
//
// A repository named explicitly by --repo has no local module to scope by:
// inspecting a release elsewhere always means the whole repository.
func scopePrefix(ctx context.Context, explicit, dir string) (string, error) {
	if explicit != "" || dir == "" {
		return "", nil
	}
	git, err := discover.FindGit(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	scope, err := discover.NewScope(git.TopLevel, dir)
	if err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	return scope.Prefix, nil
}

// runDiff compares two releases.
//
// Both sides are read from manifests rather than from the repository, so this
// works against releases of projects that are not checked out, and says what
// the artifacts actually did rather than what the commit messages claimed.
func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to compare in as owner/name (default: this repository's origin)")
	format := fs.String("format", "text", "output format: text, md, or json")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *format != "text" && *format != "md" && *format != "json" {
		return errUsage(fmt.Sprintf("letsgo diff: unknown --format %q; want text, md, or json", *format))
	}
	if fs.NArg() == 0 || fs.NArg() > 2 {
		return errUsage("letsgo diff <from> [to]\n" +
			"  each side is a tag, or a path to a letsgo.json; to defaults to the latest release")
	}

	ctx := context.Background()

	// The forge is only consulted for sides given as tags, so comparing two
	// local manifests needs neither a network nor a token.
	var (
		client *github.Client
		repo   github.Repo
	)
	from, to := fs.Arg(0), fs.Arg(1)
	if !isManifestPath(from) || !isManifestPath(to) {
		var err error
		if repo, _, err = targetRepo(ctx, *repoFlag); err != nil {
			return err
		}
		tokenValue, _ := plan.Token(*token)
		client = github.New(tokenValue)
		client.UserAgent = "letsgo/" + version
	}

	before, err := loadManifest(ctx, client, repo, from)
	if err != nil {
		return err
	}
	after, err := loadManifest(ctx, client, repo, to)
	if err != nil {
		return err
	}

	return printDiff(diff.Compare(before, after), *format)
}

// printDiff renders a comparison in the requested format. format is already
// validated by the time this runs.
func printDiff(result *diff.Result, format string) error {
	switch format {
	case "md":
		fmt.Print(result.Markdown())
	case "json":
		data, err := result.JSON()
		if err != nil {
			return fmt.Errorf("letsgo diff: %w", err)
		}
		fmt.Println(string(data))
	default:
		fmt.Print(result)
	}
	return nil
}

// loadManifest resolves one side of a diff, which is either a file on disk or
// a tag on the forge. An empty reference means the latest release.
func loadManifest(ctx context.Context, client *github.Client, repo github.Repo, ref string) (*manifest.Manifest, error) {
	if isManifestPath(ref) {
		return manifest.Read(ref)
	}
	return diff.Fetch(ctx, client, repo, ref)
}

// isManifestPath reports whether a reference names a local manifest rather
// than a tag. Tags do not exist on disk, so the file system decides: this
// keeps a tag named like a path from being misread, and the reverse.
func isManifestPath(ref string) bool {
	if ref == "" {
		return false
	}
	info, err := os.Stat(ref)
	return err == nil && info.Mode().IsRegular()
}

func runTag(args []string) error {
	fs := flag.NewFlagSet("tag", flag.ExitOnError)
	yes := fs.Bool("yes", false, "create the tag without asking")
	warranted := fs.Bool("warranted", false,
		"tag only if a commit or an API change calls for a release")
	major := fs.Bool("major", false, "force a major bump")
	minor := fs.Bool("minor", false, "force a minor bump")
	patch := fs.Bool("patch", false, "force a patch bump")
	pre := fs.Bool("pre", false, "propose a prerelease (-rc.N) instead of a stable version")
	jsonOutput := fs.Bool("json", false, "print the proposal as JSON, without creating a tag")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ctx := context.Background()

	module, err := discover.FindModule(".")
	if err != nil {
		return err
	}
	git, err := discover.FindGit(ctx, module.Dir)
	if err != nil {
		return err
	}
	if !*jsonOutput && !git.Clean {
		return fmt.Errorf("uncommitted changes; a tag names a commit, so commit first")
	}
	scope, err := discover.NewScope(git.TopLevel, module.Dir)
	if err != nil {
		return err
	}

	tags, err := discover.Tags(ctx, module.Dir, scope.Prefix)
	if err != nil {
		return err
	}
	previous, _ := scope.LatestStableTag(tags, git.Tags...)

	proposal, err := proposeVersion(ctx, module, scope, previous, forced(*major, *minor, *patch))
	if err != nil {
		return err
	}
	if *pre {
		proposal.Next = nextPrerelease(tags, scope.Prefix, proposal.Next)
	}

	if *jsonOutput {
		return printTagJSON(proposal)
	}

	return createTag(ctx, module.Dir, scope.Prefix, proposal, previous, *warranted, *yes)
}

// printTagJSON is `letsgo tag --json`'s whole job: the proposal, wire-formed,
// and nothing else — no confirmation, no write.
func printTagJSON(proposal bump.Proposal) error {
	data, err := proposal.JSON()
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// createTag reports the proposal as text, then creates the tag unless
// --warranted finds nothing to signal a release or the operator declines.
func createTag(
	ctx context.Context, dir, prefix string, proposal bump.Proposal, previous string, warranted, yes bool,
) error {
	tag := prefix + proposal.Next

	reportProposal(proposal, previous)

	// Unattended, the absence of a signal is an answer: nothing here claims to
	// be a release, so making one would put a version on a commit whose author
	// did not ask for it.
	if warranted && !proposal.Signalled() {
		fmt.Println("\n  nothing was tagged: no commit or API change calls for a release")
		return nil
	}

	if discover.TagExists(ctx, dir, tag) {
		return fmt.Errorf("%s already exists", tag)
	}
	if !yes && !confirm(tag) {
		fmt.Println("\n  nothing was tagged")
		return nil
	}

	if err := discover.CreateTag(ctx, dir, tag, tag); err != nil {
		return err
	}
	fmt.Printf("\n  tagged %s\n  push it with: git push origin %s\n", tag, tag)
	return nil
}

// nextPrerelease appends an auto-incrementing "-rc.N" suffix to a proposed
// base version. It scans the existing tags for the highest N already used
// under that exact base, so repeated --pre runs advance rc.1, rc.2, ...
// instead of colliding on the same candidate.
func nextPrerelease(tags []string, prefix, base string) string {
	want := prefix + base + "-rc."
	n := 0
	for _, tag := range tags {
		suffix, ok := strings.CutPrefix(tag, want)
		if !ok {
			continue
		}
		if v, err := strconv.Atoi(suffix); err == nil && v > n {
			n = v
		}
	}
	return fmt.Sprintf("%s-rc.%d", base, n+1)
}

// forced returns the level a flag demands, or bump.None for no flag.
func forced(major, minor, patch bool) bump.Level {
	switch {
	case major:
		return bump.Major
	case minor:
		return bump.Minor
	case patch:
		return bump.Patch
	default:
		return bump.None
	}
}

// proposeVersion gathers both signals and combines them.
//
// previous is the real git tag, prefixed exactly as it exists in the
// repository — Commits and checkoutForDiff need that to resolve it — but the
// version bump.Propose computes is a plain "vX.Y.Z", so scope.Prefix is
// stripped before it reaches the semver parser.
func proposeVersion(
	ctx context.Context, module discover.Module, scope discover.Scope, previous string, force bump.Level,
) (bump.Proposal, error) {
	previousVersion := strings.TrimPrefix(previous, scope.Prefix)

	if force != bump.None {
		return bump.Propose(previousVersion, module.Path,
			bump.Signal{Source: "you", Level: force, Detail: "requested on the command line"})
	}

	nested, err := discover.NestedModuleDirs(module.Dir)
	if err != nil {
		return bump.Proposal{}, err
	}
	commits, err := discover.Commits(ctx, module.Dir, previous, "HEAD", nested...)
	if err != nil {
		return bump.Proposal{}, err
	}
	notes := changelog.Build(previous, "", commits)

	// The API signal needs an earlier tree to compare against, and something
	// importable to compare. Whatever stopped it is carried into the signal
	// rather than swallowed: a signal that dropped out leaves the version
	// decided by commit messages alone, and the report should say so.
	var (
		changes []gate.Change
		apiErr  = errors.New("no earlier release to compare against")
	)
	if previous != "" {
		old, cleanup, err := checkoutForDiff(ctx, module.Dir, previous, scope.Dir)
		if err != nil {
			apiErr = err
		} else {
			defer cleanup()
			changes, apiErr = gate.APIDiff(ctx, old, module.Dir)
		}
	}

	return bump.Propose(previousVersion, module.Path,
		bump.FromAPI(changes, apiErr),
		bump.FromCommits(notes.Entries))
}

// checkoutForDiff checks out tag into a scratch worktree and returns the
// module's own directory within it — a worktree always holds the whole
// repository, so a module nested in it (relDir, slash-separated) is compared
// at <worktree>/relDir, never at the worktree's own root.
func checkoutForDiff(ctx context.Context, repoDir, tag, relDir string) (string, func(), error) {
	base, err := os.MkdirTemp("", "letsgo-tag-")
	if err != nil {
		return "", nil, fmt.Errorf("letsgo: scratch directory: %w", err)
	}
	worktree := filepath.Join(base, "previous")
	if err := discover.AddWorktree(ctx, repoDir, worktree, tag); err != nil {
		_ = os.RemoveAll(base)
		return "", nil, err
	}
	return filepath.Join(worktree, filepath.FromSlash(relDir)), func() {
		_ = discover.RemoveWorktree(ctx, repoDir, worktree)
		_ = os.RemoveAll(base)
	}, nil
}

func reportProposal(p bump.Proposal, previous string) {
	from := previous
	if from == "" {
		from = "(no earlier tag)"
	}
	fmt.Printf("%s \u2192 %s  (%s)\n\n", from, p.Next, p.Level)

	width := 0
	for _, s := range p.Signals {
		width = max(width, len(s.Source))
	}
	for _, s := range p.Signals {
		fmt.Printf("  %-*s  %-6s %s\n", width, s.Source, s.Level, s.Detail)
	}

	// Worth showing rather than resolving silently: the two signals measure
	// different things, and where they differ one of them is usually telling
	// you something about the change you did not intend.
	if p.Disagree() {
		fmt.Printf("\n  the signals disagree; the larger is used\n")
	}
	for _, note := range p.Notes {
		fmt.Printf("\n  ! %s\n", note)
	}
}

func confirm(version string) bool {
	fmt.Printf("\n  create tag %s? [y/N] ", version)
	return readYes()
}

// readYes reads one answer from the terminal. Anything but an explicit yes is
// a no, including an unreadable stdin: a prompt nobody saw must not be taken
// as agreement.
func readYes() bool {
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// errUsage reports a command invoked with the wrong arguments.
func errUsage(usage string) error {
	return fmt.Errorf("usage: %s", usage)
}

// releaseTag is the tag a release is published under.
//
// A real release always has one, because the plan will not pass without it. A
// rehearsal does not, so the version supplies it: showing an empty tag name
// would misrepresent the call being rehearsed, and the point of a rehearsal is
// that it does not misrepresent anything.
func releaseTag(p *plan.Plan) string {
	if p.Tag != "" {
		return p.Tag
	}
	return "v" + p.Version
}

// releaseTitle is the release's display name: the tag alone for a root
// module, unchanged from every single-module repository today, or the
// module's own directory plus the version for a scoped one — so two modules
// in the same repository read apart on the releases page instead of both
// showing a bare tag with an unlabeled prefix.
func releaseTitle(p *plan.Plan) string {
	tag := releaseTag(p)
	if p.Scope.Dir == "" {
		return tag
	}
	return p.Scope.Dir + " " + strings.TrimPrefix(tag, p.Scope.Prefix)
}

// notesMode also treats a disabled changelog as append-with-nothing-generated,
// so resuming a release with `disable changelog` set never blanks an existing
// description the way replacing it with empty notes would.
func notesMode(appendNotes, changelogEnabled bool) publish.NotesMode {
	if appendNotes || !changelogEnabled {
		return publish.NotesAppend
	}
	return publish.NotesReplace
}

// fileSum is the sha256 of a file, which for the manifest is what identifies a
// release.
func fileSum(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}

// fingerprint renders the collapsed block that identifies a release by its
// manifest. It is release notes only: the manifest cannot contain it, since
// the manifest is what it is made from.
func fingerprint(tag string, sum []byte) string {
	return fmt.Sprintf("\n<details><summary>Manifest fingerprint</summary>\n\n```\n%s\n```\n\n`sha256:%s`\n\n```\n%s```\n\n</details>\n",
		randomart.Render(sum, randomart.Title(tag)), hex.EncodeToString(sum), pgpwords.Rows(pgpwords.Encode(sum)))
}

// releaseNotes builds the changelog for everything since the previous tag,
// plus the collapsed "what shipped" section comparing this release's
// manifest against that same previous release, then the manifest's
// fingerprint.
//
// A shallow checkout is the normal shape of a CI clone, so the history is
// fetched from the forge rather than demanded of the caller.
func releaseNotes(
	ctx context.Context, p *plan.Plan, client *github.Client, repo github.Repo, current *manifest.Manifest,
	manifestSum []byte,
) (string, error) {
	if !p.Features.On("changelog") {
		return "", nil
	}
	if p.Git.Shallow {
		fmt.Println("  shallow clone; reading history from the forge")
	}

	previous, commits, err := changelog.Collect(ctx, changelog.Source{
		Dir:     p.Module.Dir,
		Tag:     p.Tag,
		Prefix:  p.Scope.Prefix,
		Shallow: p.Git.Shallow,
		Client:  client,
		Repo:    repo,
	})
	if err != nil {
		return "", err
	}
	notes := changelog.Build(previous, p.Tag, commits).WithAPIChanges(p.APIChanges).Markdown()
	extra, err := extraNotes(ctx, p, client, repo, previous, current, manifestSum)
	if err != nil {
		return "", err
	}
	return notes + extra, nil
}

// extraNotes are the sections that follow the changelog: what shipped against
// the previous release, then the manifest's fingerprint. `letsgo release` and
// `letsgo promote` both append them, so either reads the same.
func extraNotes(
	ctx context.Context, p *plan.Plan, client *github.Client, repo github.Repo, previous string,
	current *manifest.Manifest, manifestSum []byte,
) (string, error) {
	var notes string
	if p.Features.On("diff-notes") {
		shipped, err := whatShipped(ctx, client, repo, previous, current, p.Required)
		if err != nil {
			return "", err
		}
		notes += shipped
	}
	if p.Features.On("randomart") && len(manifestSum) > 0 {
		notes += fingerprint(p.Tag, manifestSum)
	}
	return notes, nil
}

// whatShipped renders the manifest-diff section against the same previous
// release the changelog above just used. A release must not fail merely
// because this supplementary section couldn't be built — a first release has
// no previous tag, and a release published before letsgo recorded a manifest
// has nothing to fetch — so any failure here is reported and the section is
// left out, rather than propagated. A release that required diff-notes asked
// for exactly that failure, so it is returned instead.
func whatShipped(
	ctx context.Context, client *github.Client, repo github.Repo, previous string, current *manifest.Manifest,
	required []string,
) (string, error) {
	strict := slices.Contains(required, "diff-notes")
	if previous == "" {
		if strict {
			return "", errors.New("diff-notes is required, but there is no previous release to compare")
		}
		return "", nil
	}
	before, err := diff.Fetch(ctx, client, repo, previous)
	if err != nil {
		if strict {
			return "", fmt.Errorf("diff-notes is required, but %s has no manifest to compare: %w", previous, err)
		}
		fmt.Printf("  ! skipped the \"what shipped\" section: %v\n", err)
		return "", nil
	}
	return diff.Compare(before, current).Notes(previous), nil
}

func sumsFrom(r *release.Result) map[string]string {
	sums := map[string]string{r.Source.Name: r.Source.SHA256}
	for _, a := range r.Manifest.Artifacts {
		sums[a.Name] = a.SHA256
	}
	if r.Manifest.Plan != nil {
		sums[release.PlanFileName] = r.Manifest.Plan.SHA256
	}
	return sums
}

// isPrerelease follows semver: a version carrying a pre-release segment is one.
func isPrerelease(p *plan.Plan) bool {
	switch p.Config.Prerelease {
	case "true":
		return true
	case "false":
		return false
	}
	base, _, _ := strings.Cut(p.Version, "+")
	return strings.Contains(base, "-")
}

// isLatest reports GitHub's make_latest value: a repository has one "latest"
// release, so auto claims it for a root module and defers for a scoped one
// (see discover.Scope), rather than fight whichever module released last for
// the badge.
func isLatest(p *plan.Plan) string {
	switch p.Config.Latest {
	case "true", "false":
		return p.Config.Latest
	}
	if p.Scope.Prefix == "" {
		return "true"
	}
	return "false"
}

// took formats an elapsed duration at a resolution a person cares about.
// Rounding everything to tenths of a second reports a plan that finished in
// forty milliseconds as "0s", which reads like the tool did nothing.
func took(started time.Time) time.Duration {
	elapsed := time.Since(started)
	if elapsed < time.Second {
		return elapsed.Round(time.Millisecond)
	}
	return elapsed.Round(100 * time.Millisecond)
}

func runFmt(args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	path := plan.ConfigFile
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	if path == "-" {
		return fmtStdin()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("letsgo: reading %s: %w", path, err)
	}
	formatted, err := formatConfig(filepath.Base(path), data)
	if err != nil {
		return err
	}
	if string(formatted) == string(data) {
		return nil
	}
	if err := os.WriteFile(path, formatted, 0o600); err != nil {
		return fmt.Errorf("letsgo: writing %s: %w", path, err)
	}
	return nil
}

// fmtStdin reads letsgo.mod from stdin and writes the formatted result to
// stdout, for format-on-save without writing the file behind the editor's
// back.
func fmtStdin() error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("letsgo: reading stdin: %w", err)
	}
	formatted, err := formatConfig(plan.ConfigFile, data)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(formatted); err != nil {
		return fmt.Errorf("letsgo: writing stdout: %w", err)
	}
	return nil
}

// formatConfig parses and decodes data before formatting it, so formatting a
// file that cannot be decoded doesn't tidy something meaningless into
// something meaningless and well-indented.
func formatConfig(name string, data []byte) ([]byte, error) {
	file, err := config.Parse(name, data)
	if err != nil {
		return nil, err
	}
	if _, err := config.Decode(file); err != nil {
		return nil, err
	}
	return file.Format(), nil
}
