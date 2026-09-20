// Command letsgo builds and publishes Go releases.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/verify"
)

// version is replaced at link time. It is declared exactly the way letsgo
// expects its users to declare it, so the tool releases itself the same way it
// releases anything else.
var version = "dev"

const usage = `letsgo builds and publishes Go releases.

usage:
  letsgo plan [--explain] [--publish]    resolve and check a release without performing one
  letsgo build [--snapshot] [-o dir]     build every artifact into dist/ without publishing
  letsgo release [--draft] [-o dir]      build and publish, resumably
  letsgo release --snapshot              rehearse a release without publishing
  letsgo verify [tag]                    rebuild a published release and compare it
  letsgo diff <from> [to]                compare two releases: size, dependencies, API
  letsgo yank <tag> [--reason "..."]     retract a release, including the go.mod directive
  letsgo tag [--major|--minor|--patch]   work out the next version and tag it
  letsgo update [--check]                update letsgo itself, verified against its manifest
  letsgo fmt [file]                      format letsgo.mod
  letsgo version                         print the version (also --version)

run a command with -h for its options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	command, args := os.Args[1], os.Args[2:]

	var err error
	switch command {
	case "plan":
		err = runPlan(args)
	case "build":
		err = runBuild(args)
	case "release":
		err = runRelease(args)
	case "verify":
		err = runVerify(args)
	case "diff":
		err = runDiff(args)
	case "yank":
		err = runYank(args)
	case "update":
		err = runUpdate(args)
	case "tag":
		err = runTag(args)
	case "fmt":
		err = runFmt(args)
	case "version", "--version", "-version", "-v":
		fmt.Println("letsgo", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "letsgo: unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "letsgo:", err)
		os.Exit(1)
	}
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

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	explain := fs.Bool("explain", false, "show where each resolved value came from")
	snapshot := fs.Bool("snapshot", false, "plan an untagged working version")
	allowDirty := fs.Bool("allow-dirty", false, "permit an unclean worktree")
	publishGates := fs.Bool("publish", false, "also check the gates a release needs: a forge, and a token that may write to it")
	analyse := fs.Bool("analyse", false, "also run the slower analysis gates, as a release does")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	started := time.Now()
	p, err := plan.Resolve(context.Background(), plan.Options{
		Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty,
		Publish: *publishGates, Token: *token, Analyse: *analyse,
	})
	if err != nil {
		return err
	}

	p.Report(os.Stdout, *explain)

	elapsed := took(started)
	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", elapsed)
		return errPlanFailed
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

	_, dir, result, err := planAndBuild(ctx, planBuildOptions{
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

func runRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	draft := fs.Bool("draft", false, "create the release without publishing it")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	out := fs.String("o", "dist", "output directory")
	skipWarm := fs.Bool("no-proxy-warm", false, "skip priming the Go module proxy")
	snapshot := fs.Bool("snapshot", false, "rehearse the release without publishing anything")
	appendNotes := fs.Bool("append-notes", false, "add the changelog after an existing release description instead of replacing it")
	allowVulnerable := fs.Bool("allow-vulnerable", false, "publish despite reachable vulnerabilities, recording which were accepted")
	allowBreaking := fs.Bool("allow-breaking", false, "publish an incompatible API change without a major version bump")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	// A rehearsal needs no forge and no token, so the gates that check for
	// them are not run.
	p, dir, result, err := planAndBuild(ctx, planBuildOptions{
		Out: *out,
		Plan: plan.Options{
			Dir: ".", Publish: !*snapshot, Token: *token, Snapshot: *snapshot,
			Analyse: true, AllowVulnerable: *allowVulnerable, AllowBreaking: *allowBreaking,
		},
		FailureNote: "nothing was built or published",
		Started:     started,
	})
	if err != nil {
		return err
	}
	fmt.Printf("\n  built %d files\n", len(result.Files))

	tokenValue, _ := plan.Token(*token)
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}

	notes, err := releaseNotes(ctx, p, client, repo)
	if err != nil {
		return err
	}

	// Everything above this line is identical in a rehearsal. Only the thing
	// that writes to the world is exchanged.
	var (
		forge  publish.Forge = client
		tapAPI brew.FileAPI  = client
	)
	if *snapshot {
		fmt.Println("\n  rehearsal: the calls below would be made, and are not")
		recorder := publish.NewRecorder(os.Stdout)
		forge, tapAPI = recorder, recorder
	}

	published, err := publish.Run(ctx, publish.Options{
		Client: forge,
		Repo:   repo,
		Dir:    dir,
		Files:  result.Files,
		Sums:   sumsFrom(result),
		Notes:  notesMode(*appendNotes),
		Release: github.ReleaseInput{
			TagName:         releaseTag(p),
			Name:            releaseTag(p),
			Body:            notes,
			Draft:           *draft || p.Config.Draft,
			Prerelease:      isPrerelease(p),
			TargetCommitish: p.Git.Commit,
		},
		Logf: func(format string, args ...any) {
			fmt.Printf("  "+format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}
	reportPublished(published)

	// After publication, because a formula names download URLs that only
	// exist once the assets are attached.
	if err := publishTap(ctx, p, result, tapAPI, client, repo); err != nil {
		return err
	}

	if err := publishImages(ctx, p, result, tokenValue, *snapshot); err != nil {
		return err
	}

	if !*skipWarm && !*snapshot && !published.Release.Draft {
		warmProxy(ctx, p)
	}

	if *snapshot {
		fmt.Printf("\n  rehearsed in %s \u00b7 nothing was published\n  artifacts: %s\n", took(started), dir)
		return nil
	}

	fmt.Printf("\n  released in %s\n  %s\n", took(started), published.Release.HTMLURL)
	return nil
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
}

// planAndBuild resolves a plan, prints its report, and builds it.
//
// Shared so that what `letsgo build` produces locally is what `letsgo release`
// uploads, decided by the same code rather than by two sequences that agree
// today.
func planAndBuild(ctx context.Context, o planBuildOptions) (*plan.Plan, string, *release.Result, error) {
	p, err := plan.Resolve(ctx, o.Plan)
	if err != nil {
		return nil, "", nil, fmt.Errorf("letsgo: %w", err)
	}
	p.Report(os.Stdout, false)

	if !p.OK() {
		fmt.Printf("\n  plan failed in %s \u00b7 %s\n", took(o.Started), o.FailureNote)
		return nil, "", nil, errPlanFailed
	}

	dir, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, "", nil, fmt.Errorf("letsgo: %w", err)
	}

	result, err := release.Build(ctx, p, dir, version, func(format string, args ...any) {
		fmt.Printf("    ! "+format+"\n", args...)
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("letsgo: %w", err)
	}
	return p, dir, result, nil
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

// warmProxy primes proxy.golang.org so `go install` works immediately.
//
// Best effort, and deliberately after publication: a proxy that is slow has
// not broken a release that is already live.
func warmProxy(ctx context.Context, p *plan.Plan) {
	if err := publish.WarmProxy(ctx, "", p.Module.Path, p.Version); err != nil {
		fmt.Printf("  ! could not prime the module proxy: %v\n", err)
		fmt.Printf("    `go install` may fail briefly until the proxy fetches %s\n", p.Tag)
		return
	}
	fmt.Println("  primed proxy.golang.org")
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to verify as owner/name (default: this repository's origin)")
	noRebuild := fs.Bool("no-rebuild", false, "compare published assets against the manifest without rebuilding")
	work := fs.String("work", "", "scratch directory (default: a temporary one)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	repo, dir, err := targetRepo(ctx, *repoFlag)
	if err != nil {
		return err
	}

	workDir := *work
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "letsgo-verify-")
		if err != nil {
			return fmt.Errorf("letsgo: scratch directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(workDir) }()
	}

	tokenValue, _ := plan.Token(*token)
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	result, err := verify.Run(ctx, verify.Options{
		Client: client, Repo: repo, Tag: fs.Arg(0),
		Dir: dir, WorkDir: workDir, SkipRebuild: *noRebuild,
		UserAgent: "letsgo/" + version,
	})
	if err != nil {
		return err
	}

	result.Report(os.Stdout)

	if !result.OK() {
		fmt.Printf("\n  %s does not verify (%s)\n", result.Tag, took(started))
		return errVerifyFailed
	}
	fmt.Printf("\n  %s verified in %s\n", result.Tag, took(started))
	return nil
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

// runDiff compares two releases.
//
// Both sides are read from manifests rather than from the repository, so this
// works against releases of projects that are not checked out, and says what
// the artifacts actually did rather than what the commit messages claimed.
func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to compare in as owner/name (default: this repository's origin)")
	if err := parseFlags(fs, args); err != nil {
		return err
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

	fmt.Print(diff.Compare(before, after))
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
	if !git.Clean {
		return fmt.Errorf("uncommitted changes; a tag names a commit, so commit first")
	}

	previous, err := discover.PreviousTag(ctx, module.Dir)
	if err != nil {
		return err
	}

	proposal, err := proposeVersion(ctx, module, previous, forced(*major, *minor, *patch))
	if err != nil {
		return err
	}

	reportProposal(proposal, previous)

	// Unattended, the absence of a signal is an answer: nothing here claims to
	// be a release, so making one would put a version on a commit whose author
	// did not ask for it.
	if *warranted && !proposal.Signalled() {
		fmt.Println("\n  nothing was tagged: no commit or API change calls for a release")
		return nil
	}

	if discover.TagExists(ctx, module.Dir, proposal.Next) {
		return fmt.Errorf("%s already exists", proposal.Next)
	}
	if !*yes && !confirm(proposal.Next) {
		fmt.Println("\n  nothing was tagged")
		return nil
	}

	if err := discover.CreateTag(ctx, module.Dir, proposal.Next, proposal.Next); err != nil {
		return err
	}
	fmt.Printf("\n  tagged %s\n  push it with: git push origin %s\n", proposal.Next, proposal.Next)
	return nil
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
func proposeVersion(ctx context.Context, module discover.Module, previous string, force bump.Level) (bump.Proposal, error) {
	if force != bump.None {
		return bump.Propose(previous, module.Path,
			bump.Signal{Source: "you", Level: force, Detail: "requested on the command line"})
	}

	commits, err := discover.Commits(ctx, module.Dir, previous, "HEAD")
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
		old, cleanup, err := checkoutForDiff(ctx, module.Dir, previous)
		if err != nil {
			apiErr = err
		} else {
			defer cleanup()
			changes, apiErr = gate.APIDiff(ctx, old, module.Dir)
		}
	}

	return bump.Propose(previous, module.Path,
		bump.FromAPI(changes, apiErr),
		bump.FromCommits(notes.Entries))
}

func checkoutForDiff(ctx context.Context, repoDir, tag string) (string, func(), error) {
	base, err := os.MkdirTemp("", "letsgo-tag-")
	if err != nil {
		return "", nil, fmt.Errorf("letsgo: scratch directory: %w", err)
	}
	dir := filepath.Join(base, "previous")
	if err := discover.AddWorktree(ctx, repoDir, dir, tag); err != nil {
		_ = os.RemoveAll(base)
		return "", nil, err
	}
	return dir, func() {
		_ = discover.RemoveWorktree(ctx, repoDir, dir)
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

func notesMode(appendNotes bool) publish.NotesMode {
	if appendNotes {
		return publish.NotesAppend
	}
	return publish.NotesReplace
}

// releaseNotes builds the changelog for everything since the previous tag.
//
// A shallow checkout is the normal shape of a CI clone, so the history is
// fetched from the forge rather than demanded of the caller.
func releaseNotes(ctx context.Context, p *plan.Plan, client *github.Client, repo github.Repo) (string, error) {
	if p.Git.Shallow {
		fmt.Println("  shallow clone; reading history from the forge")
	}

	previous, commits, err := changelog.Collect(ctx, changelog.Source{
		Dir:     p.Module.Dir,
		Tag:     p.Tag,
		Shallow: p.Git.Shallow,
		Client:  client,
		Repo:    repo,
	})
	if err != nil {
		return "", err
	}
	return changelog.Build(previous, p.Tag, commits).WithAPIChanges(p.APIChanges).Markdown(), nil
}

func sumsFrom(r *release.Result) map[string]string {
	sums := map[string]string{r.Source.Name: r.Source.SHA256}
	for _, a := range r.Manifest.Artifacts {
		sums[a.Name] = a.SHA256
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

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("letsgo: reading %s: %w", path, err)
	}
	file, err := config.Parse(filepath.Base(path), data)
	if err != nil {
		return err
	}
	// Decoding is not needed to format, but formatting a file that cannot be
	// decoded would tidy something meaningless into something meaningless and
	// well-indented.
	if _, err := config.Decode(file); err != nil {
		return err
	}

	formatted := file.Format()
	if string(formatted) == string(data) {
		return nil
	}
	if err := os.WriteFile(path, formatted, 0o600); err != nil {
		return fmt.Errorf("letsgo: writing %s: %w", path, err)
	}
	return nil
}
