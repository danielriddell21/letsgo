// Package promote rebuilds a prerelease as a stable release.
//
// `letsgo promote v1.3.0-rc.1` runs five steps, each depending on the last
// having succeeded except the first, which always runs: restore the RC to a
// prerelease, tag its commit with the stable version, rebuild at that tag and
// compare against the RC's own manifest, create the stable release from the
// rebuilt assets, and publish Brew and Docker (the last of which is the
// caller's job — see cmd/letsgo/promote.go — because it reuses the same
// unexported helpers `letsgo release` does).
//
// See docs/hld/promote.md and docs/pbs/promote.md for the full specification.
package promote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/internal/yank"
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

	Logf func(format string, args ...any)
}

// Result records what a promotion did.
type Result struct {
	StableTag string

	// RC is the RC's own release, restored to a prerelease.
	RC *github.Release

	// Plan and Build describe the stable release exactly as `letsgo
	// release` would report them, so the caller can run the same
	// tap/image publishing step 5 needs without promote duplicating it.
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

	stableTag, err := stableTagFor(o.RCTag, o.Prefix)
	if err != nil {
		return nil, err
	}

	rc, err := findRelease(ctx, o, o.RCTag)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		return nil, fmt.Errorf("promote: %s has no release tagged %s", o.Repo, o.RCTag)
	}
	if rc.Draft {
		return nil, fmt.Errorf("promote: the release for %s is a draft; publish it before promoting", o.RCTag)
	}
	if yank.IsRetracted(rc.Body) {
		return nil, fmt.Errorf("promote: %s is yanked and cannot be promoted", o.RCTag)
	}

	existing, err := findRelease(ctx, o, stableTag)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("promote: %s already exists; refusing to run again", stableTag)
	}

	rcManifest, rcManifestSHA256, err := fetchManifest(ctx, o, rc)
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

	// Step 4: stamp promoted_from, rebuild the notes, and create the
	// release. publish.Run is what makes this resumable (PR-15): it adopts
	// an existing release and re-verifies each asset rather than failing
	// because one is already there.
	built.Manifest.PromotedFrom = &manifest.PromotedFrom{Tag: o.RCTag, ManifestSHA256: rcManifestSHA256}
	if err := rewriteManifest(built); err != nil {
		return nil, err
	}

	notes, err := buildNotes(ctx, o, stableTag, p)
	if err != nil {
		return nil, err
	}

	published, err := publish.Run(ctx, publish.Options{
		Client: o.Client,
		Repo:   o.Repo,
		Dir:    built.Dir,
		Files:  built.Files,
		Sums:   sumsFrom(built),
		Notes:  notesMode(o.AppendNotes),
		Release: github.ReleaseInput{
			TagName:         stableTag,
			Name:            stableTag,
			Body:            notes,
			Draft:           false,
			Prerelease:      false,
			MakeLatest:      isLatest(p),
			TargetCommitish: rcManifest.Commit,
		},
		Logf: logf,
	})
	if err != nil {
		return nil, err
	}

	return &Result{
		StableTag: stableTag, RC: restored, Plan: p, Build: built, Published: published,
	}, nil
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

// findRelease looks a release up by tag, drafts included.
//
// ReleaseByTag and LatestRelease both exclude drafts, which is right for
// almost everything that reads a release and wrong here: refusing a draft
// RC (PR-14) requires seeing it in the first place.
func findRelease(ctx context.Context, o Options, tag string) (*github.Release, error) {
	releases, err := o.Client.ListReleases(ctx, o.Repo)
	if err != nil {
		return nil, err
	}
	for _, r := range releases {
		if r.TagName == tag {
			return &r, nil
		}
	}
	return nil, nil
}

// fetchManifest downloads the RC's own manifest and the digest of exactly
// those bytes, for promoted_from.manifest_sha256 and for step 2's commit.
func fetchManifest(ctx context.Context, o Options, rc *github.Release) (*manifest.Manifest, string, error) {
	asset, ok := rc.Asset(manifest.FileName)
	if !ok {
		return nil, "", fmt.Errorf(
			"promote: the %s release has no %s; only a release letsgo published can be promoted",
			rc.TagName, manifest.FileName)
	}
	data, err := o.Client.DownloadAsset(ctx, o.Repo, asset.ID)
	if err != nil {
		return nil, "", err
	}
	m, err := manifest.Decode(data)
	if err != nil {
		return nil, "", fmt.Errorf("promote: decoding %s's manifest: %w", rc.TagName, err)
	}
	sum := sha256.Sum256(data)
	return m, hex.EncodeToString(sum[:]), nil
}

// restoreRC is step 1.
func restoreRC(ctx context.Context, o Options, rc *github.Release) (*github.Release, error) {
	updated, err := o.Client.UpdateRelease(ctx, o.Repo, rc.ID, github.ReleaseInput{
		TagName:    rc.TagName,
		Name:       rc.Name,
		Body:       rc.Body,
		Draft:      rc.Draft,
		Prerelease: true,
		MakeLatest: "false",
	})
	if err != nil {
		return nil, fmt.Errorf("promote: restoring %s: %w", rc.TagName, err)
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
	path := filepath.Join(built.Dir, manifest.FileName)
	if err := built.Manifest.Write(path); err != nil {
		return err
	}

	sums := make([]build.Sum, 0, len(built.Files))
	for _, name := range built.Files {
		if name == build.ChecksumFile {
			continue
		}
		sum, err := sha256File(filepath.Join(built.Dir, name))
		if err != nil {
			return err
		}
		sums = append(sums, build.Sum{Name: name, SHA256: sum})
	}
	_, err := build.WriteChecksums(built.Dir, sums)
	return err
}

// buildNotes assembles the stable release's description: everything since
// the previous stable (PR-13's cumulative half), plus a collapsed
// "Prerelease history" rebuilt from the RC tags themselves rather than
// copied from their old descriptions, so a hand edit to an RC's notes is
// never carried into the release that supersedes it.
func buildNotes(ctx context.Context, o Options, stableTag string, p *plan.Plan) (string, error) {
	previous, commits, err := changelog.Collect(ctx, changelog.Source{
		Dir: o.Dir, Tag: stableTag, Prefix: o.Prefix,
		Shallow: o.Shallow, Client: o.Client, Repo: o.Repo,
	})
	if err != nil {
		return "", err
	}
	notes := changelog.Build(previous, stableTag, commits).WithAPIChanges(p.APIChanges).Markdown()

	history, err := prereleaseHistory(ctx, o, stableTag)
	if err != nil {
		return "", err
	}
	if history == "" {
		return notes, nil
	}
	return notes + "\n<details><summary>Prerelease history</summary>\n\n" + history + "</details>\n", nil
}

func sumsFrom(built *release.Result) map[string]string {
	sums := map[string]string{built.Source.Name: built.Source.SHA256}
	for _, a := range built.Manifest.Artifacts {
		sums[a.Name] = a.SHA256
	}
	// The manifest's digest changed the moment PromotedFrom was stamped onto
	// it, after release.Build already returned; what it recorded of itself
	// is stale, so the file on disk is asked instead.
	if sum, err := sha256File(filepath.Join(built.Dir, manifest.FileName)); err == nil {
		sums[manifest.FileName] = sum
	}
	return sums
}

func notesMode(appendNotes bool) publish.NotesMode {
	if appendNotes {
		return publish.NotesAppend
	}
	return publish.NotesReplace
}

// isLatest mirrors cmd/letsgo/main.go's isLatest: a repository has one
// "latest" badge, so a scoped (monorepo) module defers to a root module's
// release for it rather than fighting over it every time either promotes.
// The HLD's "marked latest" (PR-11) is read as inheriting the same rule an
// ordinary stable release already follows, not as overriding it for a
// module that opted out of claiming "latest" at all.
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

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("promote: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("promote: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
