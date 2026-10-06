// Package releaser owns the release flow: resolve a plan, build it, hold the
// build to a saved plan, guard the forge, then publish or rehearse.
//
// It sits above plan, release (assembly), apply and publication, which keeps
// each of them what CONTEXT.md says it is: a Publication starts from a built
// Release and excludes the build, so the sequence that joins them cannot live
// in publication, and apply stays plan enforcement.
//
// Clients are injected, as ADR-0022 requires: the caller resolves the
// credentials once and passes the forge, tap and release clients and the token
// value in Options. releaser never reads the environment or the machine config
// itself. Progress goes to Options.Log, and each call returns a Result for the
// caller to print the final line from.
//
// See ADR-0026.
package releaser

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/forgerelease"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/manifest"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// ErrPlanFailed marks a failure already reported in full by the plan output,
// so the caller does not print a second, vaguer version of the same thing.
var ErrPlanFailed = errors.New("plan failed")

// Clients are where a release reads from and writes to.
type Clients struct {
	// Read reads the forge: the repository's description, and the history the
	// release notes are written from. Nil means the caller has no forge to
	// read, as `letsgo build` has none.
	Read *github.Client

	// Release is where the release is created and published, and Tap where
	// the formula is written. They may be the same client as Read, or ones
	// with credentials of their own.
	Release forgerelease.Forge
	Tap     brew.FileAPI
}

// require reports a missing client, which a release cannot do without.
func (c Clients) require() error {
	switch {
	case c.Read == nil:
		return errors.New("releaser: Clients.Read is required")
	case c.Release == nil:
		return errors.New("releaser: Clients.Release is required")
	case c.Tap == nil:
		return errors.New("releaser: Clients.Tap is required")
	}
	return nil
}

// Options is everything a call needs. Each call uses the fields its comment
// names.
type Options struct {
	// Plan says how the plan is resolved. It carries the Dir the module is in
	// and the credentials the plan's own gates check.
	Plan plan.Options

	// Clients is used by Release and Diff; Build reads Clients.Read only to
	// describe the repository, when it is set.
	Clients Clients

	// Token is the credential the container registry is written with.
	Token string

	// ToolVersion is recorded in the manifest as the tool that built it.
	ToolVersion string

	// Out is the directory a build is written to.
	Out string

	// Log receives progress; nil discards it. Warn receives diagnostics, such
	// as what the release notes skipped; nil discards them.
	Log, Warn io.Writer

	// Release only.

	// Applied, when set, is the plan the release must keep to: it is held to
	// before anything is published, and only what it lists is written.
	Applied *plandiff.File

	// Draft creates the release without publishing it. Snapshot rehearses the
	// release: the same decisions, no writes. AppendNotes adds the changelog
	// after an existing release description instead of replacing it.
	Draft, Snapshot, AppendNotes bool
}

// Result is what a call did.
type Result struct {
	Plan *plan.Plan

	// Dir is where the release was built.
	Dir string

	// Build is the built release.
	Build *release.Result

	// Info is what the repository said about itself, read when there is a tap
	// to write a formula into; nil otherwise.
	Info *github.RepoInfo

	// URL is the published release's page; empty for a rehearsal or a build.
	URL string

	// Snapshot reports a rehearsal: nothing was published.
	Snapshot bool

	// Took is how long the call took.
	Took time.Duration
}

// Took is how long it has been since started, rounded for display: to the
// millisecond under a second, to a tenth of a second above it. Rounding
// everything to tenths reports a plan that finished in forty milliseconds as
// "0s", which reads like the tool did nothing.
func Took(started time.Time) time.Duration {
	elapsed := time.Since(started)
	if elapsed < time.Second {
		return elapsed.Round(time.Millisecond)
	}
	return elapsed.Round(100 * time.Millisecond)
}

func (o Options) log(format string, args ...any) {
	if o.Log != nil {
		fmt.Fprintf(o.Log, format+"\n", args...)
	}
}

func (o Options) out() io.Writer {
	if o.Log != nil {
		return o.Log
	}
	return io.Discard
}

// Build resolves a plan, reports it, and builds it: what `letsgo build`
// produces locally is what Release uploads, decided by the same code rather
// than by two sequences that agree today.
func Build(ctx context.Context, o Options) (*Result, error) {
	started := time.Now()
	built, err := build(ctx, o, started, "nothing was built")
	if err != nil {
		return nil, err
	}
	return &Result{Plan: built.plan, Dir: built.dir, Build: built.result, Info: built.info, Took: Took(started)}, nil
}

type built struct {
	plan   *plan.Plan
	dir    string
	result *release.Result
	info   *github.RepoInfo
}

// build is the resolve-report-build sequence both Build and Release open
// with. failureNote says what did not happen when the plan fails, which
// differs between building and releasing.
func build(ctx context.Context, o Options, started time.Time, failureNote string) (*built, error) {
	p, err := plan.Resolve(ctx, o.Plan)
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	p.Report(o.out(), false)

	if !p.OK() {
		o.log("\n  plan failed in %s · %s", Took(started), failureNote)
		return nil, ErrPlanFailed
	}

	dir, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}

	// A rehearsal needs no forge and no token, so the gates that check for
	// them are not run. The repository's description and licence are read
	// regardless: a tap-files plugin's cask needs them exactly as a formula
	// does, and a rehearsal has to reach every decision a real run reaches.
	var info *github.RepoInfo
	if o.Clients.Read != nil {
		info = RepoInfo(ctx, o.Clients.Read, p, o.Log)
	}

	result, err := release.Build(ctx, release.BuildOptions{
		Plan: p, Dir: dir, ToolVersion: o.ToolVersion, Repo: publication.ReleaseRepoInfo(info),
		Warnf: func(format string, args ...any) { o.log("    ! "+format, args...) },
	})
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	return &built{plan: p, dir: dir, result: result, info: info}, nil
}

// WantsRepoInfo reports whether a release should read the repository's
// description before building: only when there is a Homebrew tap to write a
// formula (or a tap-files plugin's cask) into, so a release with none never
// touches the endpoint.
func WantsRepoInfo(p *plan.Plan) bool {
	return p.HasTap() && p.HasRepo()
}

// RepoInfo reads the repository's description, licence and homepage for a
// formula or cask, but only when there is a tap to write one into. It is nil
// otherwise, and when the read fails (see DescribeRepo).
func RepoInfo(ctx context.Context, client *github.Client, p *plan.Plan, log io.Writer) *github.RepoInfo {
	if !WantsRepoInfo(p) {
		return nil
	}
	return DescribeRepo(ctx, client, github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}, log)
}

// DescribeRepo reads the description and licence the formula should carry.
//
// Failure is not fatal: `desc` and `license` are optional in a formula, and a
// release should not stop because a metadata endpoint did. It is reported to
// log (nil discards it) and returns nil, so the formula is rendered without
// them.
func DescribeRepo(ctx context.Context, client *github.Client, repo github.Repo, log io.Writer) *github.RepoInfo {
	info, err := client.Repository(ctx, repo)
	if err != nil {
		if log != nil {
			fmt.Fprintf(log, "  ! could not read %s's description for the formula: %v\n", repo, err)
		}
		return nil
	}
	return info
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

// manifestFile is the manifest's path in a built release.
func manifestFile(dir string) string { return filepath.Join(dir, manifest.FileName) }

// PlanOptions is Options plus what a plan call needs.
type PlanOptions struct {
	Options

	// Explain shows where each resolved value came from.
	Explain bool

	// Diff also builds into a scratch directory and reads the forge, to say
	// what a release would change there.
	Diff bool
}

// Planned is a resolved plan and, when asked for, what releasing it would
// change.
type Planned struct {
	Plan *plan.Plan

	// Diff is nil unless PlanOptions.Diff was set.
	Diff *apply.Diff

	// Started is when the call began, for the caller's own timing line.
	Started time.Time
}

// Plan resolves a plan and reports it: `letsgo plan`, and the first step of
// `letsgo apply` with no plan file. A plan that fails its gates returns
// ErrPlanFailed after saying so; nothing was built.
func Plan(ctx context.Context, o PlanOptions) (*Planned, error) {
	started := time.Now()

	p, err := plan.Resolve(ctx, o.Plan)
	if err != nil {
		return nil, err
	}
	p.Report(o.out(), o.Explain)

	if !p.OK() {
		o.log("\n  plan failed in %s · nothing was built", Took(started))
		return nil, ErrPlanFailed
	}

	planned := &Planned{Plan: p, Started: started}
	if o.Diff {
		planned.Diff, err = Diff(ctx, p, o.Options)
		if err != nil {
			return nil, err
		}
	}
	return planned, nil
}
