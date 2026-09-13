// Package verify rebuilds a published release and compares it against what
// was actually shipped.
//
// This is the claim the rest of the tool exists to support. A release is a
// pure function of a commit, and that is only worth asserting if anyone can
// check it — on their own hardware, without trusting the machine that built
// it.
//
// Two independent things are established. The published assets are compared
// against the manifest, which shows that what is downloadable is what the
// release describes. The artifacts are then rebuilt from source and compared
// against the manifest, which shows that what the release describes came from
// the source it names. Together they connect the source to the download; each
// alone proves considerably less.
package verify

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// Status is the outcome of one check.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	Warn Status = "warn"
	Skip Status = "skip"
)

func (s Status) symbol() string {
	switch s {
	case Pass:
		return "✓"
	case Fail:
		return "✗"
	case Warn:
		return "!"
	default:
		return "·"
	}
}

// Check is one verification result.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Options describe a verification.
type Options struct {
	Client *github.Client
	Repo   github.Repo

	// Tag is the release to verify. Empty means the most recent release.
	Tag string

	// Dir is a local checkout to rebuild from. When it does not contain the
	// released commit, the release's own source archive is used instead.
	Dir string

	// WorkDir is scratch space for the rebuild.
	WorkDir string

	// SkipRebuild compares the published assets against the manifest without
	// rebuilding, which needs no toolchain and no source.
	SkipRebuild bool
}

// Result is what verification established.
type Result struct {
	Tag      string
	Manifest *manifest.Manifest

	// SourceFrom describes where the rebuilt source came from, because it
	// changes what the result means: a local checkout ties the binaries to the
	// repository, while the release's own source archive ties them only to
	// itself.
	SourceFrom string

	Checks []Check
}

// OK reports whether every check passed.
func (r *Result) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

func (r *Result) add(name string, status Status, format string, args ...any) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
}

// Report writes a human-readable summary.
func (r *Result) Report(w io.Writer) {
	fmt.Fprintf(w, "%s\n\n", r.Tag)

	width := 0
	for _, c := range r.Checks {
		width = max(width, len(c.Name))
	}
	for _, c := range r.Checks {
		lines := strings.Split(c.Detail, "\n")
		fmt.Fprintf(w, "  %s %-*s  %s\n", c.Status.symbol(), width, c.Name, lines[0])
		for _, extra := range lines[1:] {
			fmt.Fprintf(w, "    %-*s  %s\n", width, "", extra)
		}
	}
}

// Run verifies a release.
func Run(ctx context.Context, o Options) (*Result, error) {
	release, err := findRelease(ctx, o)
	if err != nil {
		return nil, err
	}

	result := &Result{Tag: release.TagName}

	m, err := fetchManifest(ctx, o, release)
	if err != nil {
		return nil, err
	}
	result.Manifest = m
	result.add("manifest", Pass, "letsgo.json describes %d artifacts, built by %s with %s",
		len(m.Artifacts), m.Builder.Tool, m.Builder.Go)

	comparePublished(result, release, m)
	checkProvenance(ctx, o, result, m)

	if o.SkipRebuild {
		result.add("rebuild", Skip, "not requested")
		return result, nil
	}
	if err := rebuild(ctx, o, result, release, m); err != nil {
		return nil, err
	}
	return result, nil
}

func findRelease(ctx context.Context, o Options) (*github.Release, error) {
	if o.Tag != "" {
		release, err := o.Client.ReleaseByTag(ctx, o.Repo, o.Tag)
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, fmt.Errorf("verify: %s has no release tagged %s", o.Repo, o.Tag)
		}
		return release, nil
	}

	release, err := o.Client.LatestRelease(ctx, o.Repo)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("verify: %s has no releases", o.Repo)
	}
	return release, nil
}

func fetchManifest(ctx context.Context, o Options, release *github.Release) (*manifest.Manifest, error) {
	asset, ok := release.Asset(manifest.FileName)
	if !ok {
		return nil, fmt.Errorf(
			"verify: release %s has no %s, so there is nothing describing what it should contain\n"+
				"  only releases published by letsgo can be verified",
			release.TagName, manifest.FileName)
	}
	data, err := o.Client.DownloadAsset(ctx, o.Repo, asset.ID)
	if err != nil {
		return nil, err
	}
	return manifest.Decode(data)
}

// comparePublished checks the assets attached to the release against what the
// manifest says they should be.
//
// The forge reports each asset's digest, so this needs no downloads: the
// question is whether the release's own description matches its contents.
func comparePublished(result *Result, release *github.Release, m *manifest.Manifest) {
	published := make(map[string]github.Asset, len(release.Assets))
	for _, a := range release.Assets {
		published[a.Name] = a
	}

	var problems []string
	checked := 0

	for _, want := range m.Artifacts {
		asset, found := published[want.Name]
		if !found {
			problems = append(problems, fmt.Sprintf("%s is described but not attached", want.Name))
			continue
		}
		got, ok := asset.SHA256()
		if !ok {
			// Without a digest from the forge there is nothing to compare
			// that downloading would not be needed for.
			continue
		}
		checked++
		if got != want.SHA256 {
			problems = append(problems,
				fmt.Sprintf("%s: published %s, manifest says %s", want.Name, short(got), short(want.SHA256)))
		}
	}

	switch {
	case len(problems) > 0:
		result.add("published assets", Fail, "%s", strings.Join(problems, "\n"))
	case checked == 0:
		result.add("published assets", Warn, "the forge reports no digests, so the attached files were not checked")
	default:
		result.add("published assets", Pass, "%d attached files match the manifest", checked)
	}
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
