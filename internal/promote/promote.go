// Package promote rebuilds a prerelease as a stable release.
//
// `letsgo promote v1.3.0-rc.1` runs five steps, each depending on the last
// having succeeded except the first, which always runs: restore the RC to a
// prerelease, tag its commit with the stable version, rebuild at that tag and
// compare against the RC's own manifest, then publish the stable release from
// the rebuilt assets, its Homebrew formula and its container image through the
// same publication module `letsgo release` uses, checksum gate included.
//
// See docs/hld/promote.md and docs/pbs/promote.md for the full specification.
package promote

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/releases/githubsource"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// Options describe a promotion.
type Options struct {
	Client *github.Client
	Repo   github.Repo

	// RCTag is the prerelease being promoted, e.g. "v1.3.0-rc.1".
	RCTag string

	// Dir is the git repository holding the module, and ModuleDir is the
	// module's own directory within it — equal to Dir for a root module,
	// as discover.Scope describes.
	Dir       string
	ModuleDir string
	Prefix    string

	// Shallow says Dir's history is incomplete, so the changelog must ask
	// the forge for commits rather than read them locally.
	Shallow bool

	// Remote is the git remote the stable tag is pushed to. Empty means
	// "origin". Pushing it is best effort: the release created in step 4
	// carries a target commit, which makes GitHub create the same tag on
	// its own, so a local push failure does not block promotion.
	Remote string

	// ToolVersion and RepoInfo are threaded straight through to
	// release.Build, exactly as `letsgo release` supplies them.
	ToolVersion string
	RepoInfo    *github.RepoInfo

	// WorkDir is scratch space for the rebuild worktree and its output. The
	// caller owns it and removes it once Run returns.
	WorkDir string

	// AppendNotes adds the generated notes after an existing description
	// instead of replacing it, exactly as `letsgo release --append-notes`
	// does — relevant only on a resumed run that already created the
	// release with hand-edited notes since.
	AppendNotes bool

	// ExtraNotes renders the sections `letsgo release` appends after the
	// changelog — what shipped, the manifest fingerprint — so a promoted
	// release reads like any other. It is given the previous release the
	// changelog used and the manifest as it will be published, promoted_from
	// included, because that is what the fingerprint names. Optional.
	ExtraNotes func(ctx context.Context, p *plan.Plan, previous string, current *manifest.Manifest, manifestSum []byte) (string, error)

	// Tap, Token and Out are what the publication writes the formula, the
	// container image and its report with, exactly as `letsgo release`
	// supplies them. Tap may be nil when the module has no tap.
	Tap   brew.FileAPI
	Token string
	Out   io.Writer

	Logf func(format string, args ...any)
}

// Result records what a promotion did.
type Result struct {
	StableTag string

	// RC is the RC's own release, restored to a prerelease.
	RC *github.Release

	// Plan and Build describe the stable release exactly as `letsgo
	// release` would report them.
	Plan  *plan.Plan
	Build *release.Result

	Published *publish.Result
}

// Run promotes an RC. See the package doc for the five steps.
func Run(ctx context.Context, o Options) (*Result, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if o.RCTag == "" {
		return nil, fmt.Errorf("promote: no tag given")
	}

	// WorkDir seeds both the rebuild worktree and its build output. The
	// rebuild step below runs `go build` with its working directory set to
	// the worktree's own module directory (see gobuild.Build), so a
	// relative WorkDir would resolve the build output against the wrong
	// directory the moment that differs from this process's cwd — which it
	// always does inside a freshly created worktree. Absolute once here
	// keeps every path derived from it correct regardless of where the
	// build subprocess's cwd ends up.
	if o.WorkDir != "" {
		abs, err := filepath.Abs(o.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("promote: resolving work directory: %w", err)
		}
		o.WorkDir = abs
	}

	stableTag, err := stableTagFor(o.RCTag, o.Prefix)
	if err != nil {
		return nil, err
	}

	rc, rcManifest, rcManifestSHA256, err := resolveRC(ctx, o, stableTag)
	if err != nil {
		return nil, err
	}

	// Step 1: unconditional, and first, because the RC *is* a prerelease
	// regardless of anything a UI flip did to it.
	logf("restoring %s to a prerelease", o.RCTag)
	restored, err := restoreRC(ctx, o, rc)
	if err != nil {
		return nil, err
	}

	// Step 2: tag the RC's own commit, not HEAD.
	logf("tagging %s on %s", stableTag, shortCommit(rcManifest.Commit))
	if err := tagCommit(ctx, o, stableTag, rcManifest.Commit, logf); err != nil {
		return nil, err
	}

	// Step 3: rebuild at that tag and compare.
	logf("rebuilding at %s", stableTag)
	p, built, err := rebuild(ctx, o, stableTag, rcManifest.Commit)
	if err != nil {
		return nil, err
	}
	if diffs := Compare(rcManifest, built.Manifest); len(diffs) > 0 {
		return nil, fmt.Errorf("promote: the rebuild does not match %s:\n  - %s",
			o.RCTag, strings.Join(diffs, "\n  - "))
	}
	logf("rebuild matches %s (version, archive names and their digests aside)", o.RCTag)

	// Step 4: stamp promoted_from, rebuild the notes, and publish. Publication
	// is what makes this resumable (PR-15): it adopts an existing release and
	// re-verifies each asset rather than failing because one is already there.
	built.Manifest.PromotedFrom = &manifest.PromotedFrom{Tag: o.RCTag, ManifestSHA256: rcManifestSHA256}
	if err := rewriteManifest(built); err != nil {
		return nil, err
	}

	notes, err := buildNotes(ctx, o, stableTag, p, built)
	if err != nil {
		return nil, fmt.Errorf("promote: building the notes: %w", err)
	}

	// A promotion is by definition a public, stable release, whatever the
	// repository's config says about drafts and prereleases.
	stable := *p.Config
	stable.Draft, stable.Prerelease = false, "false"
	p.Config = &stable

	published, err := publication.Publish(ctx, publication.Options{
		Plan:   p,
		Result: built,
		Dir:    built.Dir,
		Repo:   o.Repo,
		Forge:  o.Client,
		Tap:    o.Tap,
		Info:   o.RepoInfo,
		Notes:  notes,
		Append: o.AppendNotes,
		Token:  o.Token,
		Out:    o.Out,
	})
	if err != nil {
		return nil, fmt.Errorf("promote: %w", err)
	}

	return &Result{
		StableTag: stableTag, RC: restored, Plan: p, Build: built, Published: published.Forge,
	}, nil
}

// resolveRC finds and validates the RC release — every refusal condition
// PR-14 names, checked before anything is written — and returns it alongside
// its own manifest and that manifest's digest, ready for step 2 onward.
//
// Split out of Run so that the refusal checks read as one block rather than
// adding their own branch to the five-step pipeline's own complexity.
func resolveRC(ctx context.Context, o Options, stableTag string) (*releases.Published, *manifest.Manifest, string, error) {
	rc, err := findRelease(ctx, o, o.RCTag)
	if err != nil {
		return nil, nil, "", err
	}
	if rc == nil {
		return nil, nil, "", fmt.Errorf("promote: %s has no release tagged %s", o.Repo, o.RCTag)
	}
	if rc.Draft {
		return nil, nil, "", fmt.Errorf("promote: the release for %s is a draft; publish it before promoting", o.RCTag)
	}
	if rc.Retracted() {
		return nil, nil, "", fmt.Errorf("promote: %s is yanked and cannot be promoted", o.RCTag)
	}

	existing, err := findRelease(ctx, o, stableTag)
	if err != nil {
		return nil, nil, "", err
	}
	if existing != nil {
		return nil, nil, "", fmt.Errorf("promote: %s already exists; refusing to run again", stableTag)
	}

	rcManifest, rcManifestSHA256, err := fetchManifest(ctx, o, rc)
	if err != nil {
		return nil, nil, "", err
	}
	return rc, rcManifest, rcManifestSHA256, nil
}

// stableTagFor derives the target tag: the RC's own tag minus its prerelease
// suffix. There is no `--to`: the HLD's own answer to that question is no,
// the target is always the RC's version with its suffix removed.
func stableTagFor(rcTag, prefix string) (string, error) {
	scope := discover.Scope{Prefix: prefix}
	rest, ok := scope.MatchesTag(rcTag)
	if !ok {
		return "", fmt.Errorf("promote: %s is not a version tag in this module's scope", rcTag)
	}
	v, ok := semver.Parse(rest)
	if !ok {
		return "", fmt.Errorf("promote: %s does not parse as a version", rcTag)
	}
	if !v.IsPrerelease() {
		return "", fmt.Errorf("promote: %s is not a prerelease; only a prerelease can be promoted", rcTag)
	}
	return fmt.Sprintf("%sv%d.%d.%d", prefix, v.Major, v.Minor, v.Patch), nil
}

// findRelease looks a release up by tag, drafts included, because refusing a
// draft RC (PR-14) requires seeing it in the first place.
func findRelease(ctx context.Context, o Options, tag string) (*releases.Published, error) {
	release, err := releases.ByTagIncludingDrafts(ctx, &githubsource.Source{Client: o.Client, Repo: o.Repo}, tag)
	if err != nil {
		return nil, fmt.Errorf("promote: listing releases: %w", err)
	}
	return release, nil
}

// fetchManifest downloads the RC's own manifest and the digest of exactly
// those bytes, for promoted_from.manifest_sha256 and for step 2's commit.
func fetchManifest(ctx context.Context, o Options, rc *releases.Published) (*manifest.Manifest, string, error) {
	m, sum, err := releases.Manifest(ctx, &githubsource.Source{Client: o.Client, Repo: o.Repo}, rc)
	if releases.IsNoManifest(err) {
		return nil, "", fmt.Errorf(
			"promote: the %s release has no %s; only a release letsgo published can be promoted",
			rc.Tag, manifest.FileName)
	}
	if err != nil {
		return nil, "", fmt.Errorf("promote: reading %s's manifest: %w", rc.Tag, err)
	}
	return m, hex.EncodeToString(sum), nil
}

// restoreRC is step 1.
func restoreRC(ctx context.Context, o Options, rc *releases.Published) (*github.Release, error) {
	updated, err := o.Client.UpdateRelease(ctx, o.Repo, rc.ID, github.ReleaseInput{
		TagName:    rc.Tag,
		Name:       rc.Name,
		Body:       rc.Body,
		Draft:      rc.Draft,
		Prerelease: true,
		MakeLatest: "false",
	})
	if err != nil {
		return nil, fmt.Errorf("promote: restoring %s: %w", rc.Tag, err)
	}
	return updated, nil
}

// tagCommit is step 2: tag the RC's commit, idempotently, and push the tag
// best effort.
func tagCommit(ctx context.Context, o Options, tag, commit string, logf func(string, ...any)) error {
	if discover.TagExists(ctx, o.Dir, tag) {
		existing, err := discover.TagCommit(ctx, o.Dir, tag)
		if err != nil {
			return fmt.Errorf("promote: %w", err)
		}
		if existing != commit {
			return fmt.Errorf("promote: %s already exists locally and points at %s, not the RC's commit %s",
				tag, shortCommit(existing), shortCommit(commit))
		}
	} else if err := discover.CreateTagAt(ctx, o.Dir, tag, commit, tag); err != nil {
		return fmt.Errorf("promote: %w", err)
	}

	remote := o.Remote
	if remote == "" {
		remote = "origin"
	}
	if err := discover.PushTag(ctx, o.Dir, remote, tag); err != nil {
		logf("! could not push %s to %s: %v (the release in step 4 will create it there)", tag, remote, err)
	}
	return nil
}

// rebuild is step 3's build half: a plan resolved at the stable tag,
// checked out at the RC's own commit, built exactly as `letsgo release`
// builds one.
//
// Analyse and Publish are both left off: PR-10 does not ask the rebuild to
// re-run the vulnerability or API gates, Compare does not look at Gates or
// APIChanges (see compare.go), and the RC's own run already recorded
// whatever those gates decided. Re-running them here would cost the slowest
// part of a release for a question promote is not asking.
func rebuild(ctx context.Context, o Options, stableTag, commit string) (*plan.Plan, *release.Result, error) {
	checkoutDir := filepath.Join(o.WorkDir, "checkout")
	_ = os.RemoveAll(checkoutDir)
	if err := discover.AddWorktree(ctx, o.Dir, checkoutDir, commit); err != nil {
		return nil, nil, fmt.Errorf("promote: %w", err)
	}
	defer func() { _ = discover.RemoveWorktree(ctx, o.Dir, checkoutDir) }()

	rel, err := filepath.Rel(o.Dir, o.ModuleDir)
	if err != nil {
		return nil, nil, fmt.Errorf("promote: %w", err)
	}
	moduleDir := filepath.Join(checkoutDir, rel)

	p, err := plan.Resolve(ctx, plan.Options{
		Dir: moduleDir,
		Tag: stableTag,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("promote: %w", err)
	}
	if !p.OK() {
		return nil, nil, fmt.Errorf("promote: the rebuilt plan failed:\n  - %s", strings.Join(planFailures(p), "\n  - "))
	}

	buildDir := filepath.Join(o.WorkDir, "build")
	result, err := release.Build(ctx, p, buildDir, o.ToolVersion, o.RepoInfo, func(format string, args ...any) {
		if o.Logf != nil {
			o.Logf("! "+format, args...)
		}
	})
	if err != nil {
		return nil, nil, fmt.Errorf("promote: %w", err)
	}
	return p, result, nil
}

func planFailures(p *plan.Plan) []string {
	var failures []string
	for _, c := range p.Checks {
		if c.Status == plan.Fail {
			failures = append(failures, fmt.Sprintf("%s: %s", c.Name, c.Detail))
		}
	}
	return failures
}

// rewriteManifest writes the manifest again now that PromotedFrom is set,
// and recomputes SHA256SUMS to match: release.Build wrote both before
// promote had anything to stamp, so both are stale the moment PromotedFrom
// is attached and have to be regenerated from what is actually on disk.
func rewriteManifest(built *release.Result) error {
	if err := built.Restamp(); err != nil {
		return fmt.Errorf("promote: %w", err)
	}
	return nil
}

// buildNotes assembles the stable release's description: everything since
// the previous stable (PR-13's cumulative half), plus a collapsed
// "Prerelease history" rebuilt from the RC tags themselves rather than
// copied from their old descriptions, so a hand edit to an RC's notes is
// never carried into the release that supersedes it.
func buildNotes(ctx context.Context, o Options, stableTag string, p *plan.Plan, built *release.Result) (string, error) {
	if !p.Features.On("changelog") {
		return "", nil
	}

	previous, commits, err := changelog.Collect(ctx, changelog.Source{
		Dir: o.Dir, Tag: stableTag, Prefix: o.Prefix,
		Shallow: o.Shallow, Client: o.Client, Repo: o.Repo,
	})
	if err != nil {
		return "", fmt.Errorf("promote: collecting the commits: %w", err)
	}
	notes := changelog.Build(previous, stableTag, commits).WithAPIChanges(p.APIChanges).Markdown()

	history, err := prereleaseHistory(ctx, o, stableTag)
	if err != nil {
		return "", err
	}
	if history != "" {
		notes += "\n<details><summary>Prerelease history</summary>\n\n" + history + "</details>\n"
	}

	if o.ExtraNotes == nil {
		return notes, nil
	}
	sum, err := manifestDigest(built)
	if err != nil {
		return "", fmt.Errorf("promote: digesting the manifest: %w", err)
	}
	extra, err := o.ExtraNotes(ctx, p, previous, built.Manifest, sum)
	if err != nil {
		return "", fmt.Errorf("promote: generating the extra notes: %w", err)
	}
	return notes + extra, nil
}

// manifestDigest is the digest of the manifest as it will be published, read
// from the file because stamping promoted_from changed what the in-memory
// copy recorded of itself.
func manifestDigest(built *release.Result) ([]byte, error) {
	hexSum, err := built.Digest(manifest.FileName)
	if err != nil {
		return nil, fmt.Errorf("promote: hashing the manifest: %w", err)
	}
	sum, err := hex.DecodeString(hexSum)
	if err != nil {
		return nil, fmt.Errorf("promote: %w", err)
	}
	return sum, nil
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
