// Command letsgo builds and publishes Go releases.
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

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
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
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	p, err := plan.Resolve(ctx, plan.Options{Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty})
	if err != nil {
		return err
	}
	p.Report(os.Stdout, false)

	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", took(started))
		return errPlanFailed
	}

	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}

	result, err := release.Build(ctx, p, dir, version, func(format string, args ...any) {
		fmt.Printf("    ! "+format+"\n", args...)
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	// A rehearsal needs no forge and no token, so the gates that check for
	// them are not run.
	p, err := plan.Resolve(ctx, plan.Options{
		Dir: ".", Publish: !*snapshot, Token: *token, Snapshot: *snapshot,
		Analyse: true, AllowVulnerable: *allowVulnerable, AllowBreaking: *allowBreaking,
	})
	if err != nil {
		return err
	}
	p.Report(os.Stdout, false)

	if !p.OK() {
		fmt.Printf("\n  plan failed in %s \u00b7 nothing was built or published\n", took(started))
		return errPlanFailed
	}

	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}

	result, err := release.Build(ctx, p, dir, version, func(format string, args ...any) {
		fmt.Printf("    ! "+format+"\n", args...)
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
	var forge publish.Forge = client
	if *snapshot {
		fmt.Println("\n  rehearsal: the calls below would be made, and are not")
		forge = publish.NewRecorder(os.Stdout)
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

	if published.NotesRefused {
		fmt.Println("  ! the release description could not be updated with this token")
	}
	fmt.Printf("  uploaded %d, skipped %d", len(published.Uploaded), len(published.Skipped))
	if len(published.Replaced) > 0 {
		fmt.Printf(", replaced %d", len(published.Replaced))
	}
	fmt.Println()

	// Best effort, and deliberately after publication: a proxy that is slow
	// has not broken a release that is already live.
	if !*skipWarm && !*snapshot && !published.Release.Draft {
		if err := publish.WarmProxy(ctx, "", p.Module.Path, p.Version); err != nil {
			fmt.Printf("  ! could not prime the module proxy: %v\n", err)
			fmt.Printf("    `go install` may fail briefly until the proxy fetches %s\n", p.Tag)
		} else {
			fmt.Println("  primed proxy.golang.org")
		}
	}

	if *snapshot {
		fmt.Printf("\n  rehearsed in %s \u00b7 nothing was published\n  artifacts: %s\n", took(started), dir)
		return nil
	}

	fmt.Printf("\n  released in %s\n  %s\n", took(started), published.Release.HTMLURL)
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to verify as owner/name (default: this repository's origin)")
	noRebuild := fs.Bool("no-rebuild", false, "compare published assets against the manifest without rebuilding")
	work := fs.String("work", "", "scratch directory (default: a temporary one)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	repo, dir, err := verifyTarget(ctx, *repoFlag)
	if err != nil {
		return err
	}

	workDir := *work
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "letsgo-verify-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(workDir)
	}

	tokenValue, _ := plan.Token(*token)
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	result, err := verify.Run(ctx, verify.Options{
		Client: client, Repo: repo, Tag: fs.Arg(0),
		Dir: dir, WorkDir: workDir, SkipRebuild: *noRebuild,
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

// verifyTarget resolves which repository to verify and, where possible, a
// local checkout to rebuild from.
//
// Verifying someone else's release is the point, so a repository outside the
// current directory is allowed; it simply cannot be rebuilt from a local
// checkout, and the report says so.
func verifyTarget(ctx context.Context, explicit string) (github.Repo, string, error) {
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := plan.ConfigFile
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
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
	return os.WriteFile(path, formatted, 0o644)
}
