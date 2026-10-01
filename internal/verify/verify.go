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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/pgpwords"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/randomart"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/releases/githubsource"
)

// auditFileName is internal/audit.FileName, duplicated rather than imported:
// audit imports this package (to reuse FetchManifest/SourceFromArchive), so
// the reverse import would cycle.
const auditFileName = "audit.json"

// planFileName is internal/release.PlanFileName, duplicated for the same
// reason as auditFileName.
const planFileName = "letsgo.plan.json"

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
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Options describe a verification.
type Options struct {
	// GoBin is the go command a rebuild runs, as gobuild.Toolchain resolved it.
	GoBin string

	Client *github.Client
	Repo   github.Repo

	// Tag is the release to verify. Empty means the most recent release.
	Tag string

	// Prefix is the module's scope prefix (see discover.Scope), empty for a
	// root module. It is only consulted when Tag is empty: it keeps "the
	// most recent release" from picking another module's release in a
	// monorepo.
	Prefix string

	// Dir is a local checkout to rebuild from. When it does not contain the
	// released commit, the release's own source archive is used instead.
	Dir string

	// WorkDir is scratch space for the rebuild.
	WorkDir string

	// SkipRebuild compares the published assets against the manifest without
	// rebuilding, which needs no toolchain and no source.
	SkipRebuild bool

	// UserAgent identifies letsgo to registries, which the forge client
	// carries separately.
	UserAgent string
}

// Result is what verification established.
type Result struct {
	Tag      string
	Repo     string
	Manifest *manifest.Manifest

	// Artifacts is what verification found out about each artifact the
	// manifest describes, for anything that presents the result per item.
	Artifacts []ArtifactResult

	// published and rebuilt record each artifact's outcome as the two
	// comparisons make it, and Artifacts is settled from them.
	published, rebuilt map[string]Status

	// ManifestSum is the sha256 of the published letsgo.json, which the
	// fingerprint drawn after a pass is made from.
	ManifestSum []byte

	// SourceFrom describes where the rebuilt source came from, because it
	// changes what the result means: a local checkout ties the binaries to the
	// repository, while the release's own source archive ties them only to
	// itself.
	SourceFrom string

	Checks []Check

	// userAgent identifies letsgo to anything this verification contacts
	// beyond the forge.
	userAgent string
}

// ArtifactResult is one artifact's outcome: Pass only when it was rebuilt and
// matched, Fail when anything about it differed, and Skip when nothing
// established either way, which is the honest reading of a check not run.
type ArtifactResult struct {
	Name   string
	Size   int64
	SHA256 string
	Status Status
}

// jsonResult is Result's wire form for `letsgo verify --json`:
// schema-versioned, so a consumer can tell which shape it's reading before
// the fields under it ever change.
type jsonResult struct {
	Schema        int      `json:"schema"`
	Tag           string   `json:"tag"`
	SourceFrom    string   `json:"sourceFrom,omitempty"`
	Checks        []Check  `json:"checks"`
	ManifestWords []string `json:"manifest_words,omitempty"`
}

// JSON renders the report for machine consumers (`letsgo verify --json`).
func (r *Result) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(jsonResult{
		Schema:     1,
		Tag:        r.Tag,
		SourceFrom: r.SourceFrom,
		Checks:     r.Checks,

		ManifestWords: r.Words(),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	return data, nil
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

// settleArtifacts derives each artifact's outcome from the two comparisons.
func (r *Result) settleArtifacts() {
	if r.Manifest == nil {
		return
	}
	r.Artifacts = r.Artifacts[:0]
	for _, a := range r.Manifest.Artifacts {
		status := Skip
		switch {
		case r.published[a.Name] == Fail || r.rebuilt[a.Name] == Fail:
			status = Fail
		case r.rebuilt[a.Name] == Pass:
			status = Pass
		}
		r.Artifacts = append(r.Artifacts, ArtifactResult{Name: a.Name, Size: a.Size, SHA256: a.SHA256, Status: status})
	}
}

func (r *Result) add(name string, status Status, format string, args ...any) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
}

// Words is the manifest digest as PGP words, for reading aloud. It is nil
// unless verification passed: nobody should read out a hash that did not
// verify.
func (r *Result) Words() []string {
	if !r.OK() || len(r.ManifestSum) == 0 {
		return nil
	}
	return pgpwords.Encode(r.ManifestSum)
}

// ReportWords writes the manifest digest as numbered rows of PGP words, when
// there is a passing verification to read out.
func (r *Result) ReportWords(w io.Writer) {
	words := r.Words()
	if words == nil {
		return
	}
	fmt.Fprintf(w, "\n  manifest sha256, read aloud:\n%s", pgpwords.Rows(words))
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

	if r.OK() && len(r.ManifestSum) > 0 {
		fmt.Fprintf(w, "\n%s\nsha256:%s\n",
			randomart.Render(r.ManifestSum, randomart.Title(r.Tag)), hex.EncodeToString(r.ManifestSum))
	}
}

// Run verifies a release.
func Run(ctx context.Context, o Options) (*Result, error) {
	release, err := findRelease(ctx, o)
	if err != nil {
		return nil, err
	}

	result := &Result{
		Tag: release.Tag, Repo: o.Repo.String(), userAgent: o.UserAgent,
		published: map[string]Status{}, rebuilt: map[string]Status{},
	}
	if result.userAgent == "" {
		result.userAgent = "letsgo"
	}

	m, sum, err := fetchManifest(ctx, o, release)
	if err != nil {
		return nil, err
	}
	result.Manifest = m
	result.ManifestSum = sum
	result.add("manifest", Pass, "letsgo.json describes %d artifacts, built by %s with %s",
		len(m.Artifacts), m.Builder.Tool, m.Builder.Go)
	reportFeatures(result, m)
	reportAudit(ctx, o, result, release)
	reportPlan(ctx, o, result, release, m)

	comparePublished(result, release, m)
	checkProvenance(ctx, o, result, m)
	checkImages(ctx, result, m)

	if o.SkipRebuild {
		result.add("rebuild", Skip, "not requested")
		result.settleArtifacts()
		return result, nil
	}
	rebuild(ctx, o, result, release, m)
	result.settleArtifacts()
	return result, nil
}

func findRelease(ctx context.Context, o Options) (*releases.Published, error) {
	src := &githubsource.Source{Client: o.Client, Repo: o.Repo}

	if o.Tag != "" {
		release, err := releases.ByTag(ctx, src, o.Tag)
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, fmt.Errorf("verify: %s has no release tagged %s", o.Repo, o.Tag)
		}
		return release, nil
	}

	release, err := releases.Latest(ctx, src, discover.Scope{Prefix: o.Prefix})
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("verify: %s has no releases", o.Repo)
	}
	return release, nil
}

// fetchManifest also returns the sha256 of the manifest as published, which is
// what identifies the release.
func fetchManifest(ctx context.Context, o Options, release *releases.Published) (*manifest.Manifest, []byte, error) {
	src := &githubsource.Source{Client: o.Client, Repo: o.Repo}
	m, sum, err := releases.Manifest(ctx, src, release)
	if releases.IsNoManifest(err) {
		return nil, nil, fmt.Errorf(
			"verify: release %s has no %s, so there is nothing describing what it should contain\n"+
				"  only releases published by letsgo can be verified",
			release.Tag, manifest.FileName)
	}
	return m, sum, err
}

// comparePublished checks the assets attached to the release against what the
// manifest says they should be.
//
// The forge reports each asset's digest, so this needs no downloads: the
// question is whether the release's own description matches its contents.
func comparePublished(result *Result, release *releases.Published, m *manifest.Manifest) {
	published := make(map[string]releases.Asset, len(release.Assets))
	for _, a := range release.Assets {
		published[a.Name] = a
	}

	var problems []string
	checked := 0

	for _, want := range m.Artifacts {
		asset, found := published[want.Name]
		if !found {
			problems = append(problems, fmt.Sprintf("%s is described but not attached", want.Name))
			result.published[want.Name] = Fail
			continue
		}
		got := asset.SHA256
		if got == "" {
			// Without a digest from the forge there is nothing to compare
			// that downloading would not be needed for.
			continue
		}
		checked++
		result.published[want.Name] = Pass
		if got != want.SHA256 {
			problems = append(problems,
				fmt.Sprintf("%s: published %s, manifest says %s", want.Name, short(got), short(want.SHA256)))
			result.published[want.Name] = Fail
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

// reportFeatures surfaces the toggles the release was built with, the same
// way the plan that produced it did: informational, not a gate, and silent
// when nothing was disabled or required.
func reportFeatures(result *Result, m *manifest.Manifest) {
	if m.Features == nil {
		return
	}

	var parts []string
	if len(m.Features.Disabled) > 0 {
		parts = append(parts, "disabled: "+strings.Join(m.Features.Disabled, ", "))
	}
	if len(m.Features.Required) > 0 {
		parts = append(parts, "required: "+strings.Join(m.Features.Required, ", "))
	}
	result.add("features", Pass, "%s", strings.Join(parts, "; "))
}

// auditEntry is the one field set reportAudit needs from an audit.json
// entry (internal/audit.Entry's own schema).
type auditEntry struct {
	Vulndb   string `json:"vulndb"`
	Status   string `json:"status"`
	Findings []struct {
		ID string `json:"id"`
	} `json:"findings"`
}

// reportAudit surfaces the release's latest audit.json entry, when one
// exists (AU-10): informational only, so an affected release still passes
// (AU-9) — a later audit re-checking against today's vulndb is not the
// release's own fault.
func reportAudit(ctx context.Context, o Options, result *Result, release *releases.Published) {
	asset, ok := release.Asset(auditFileName)
	if !ok {
		return
	}
	data, err := o.Client.DownloadAsset(ctx, o.Repo, asset.ID)
	if err != nil {
		result.add("audit", Warn, "could not read %s: %v", auditFileName, err)
		return
	}
	var record struct {
		Audits []auditEntry `json:"audits"`
	}
	if err := json.Unmarshal(data, &record); err != nil || len(record.Audits) == 0 {
		return
	}

	last := record.Audits[len(record.Audits)-1]
	if last.Status != "affected" {
		result.add("audit", Pass, "clean (as of %s)", last.Vulndb)
		return
	}
	ids := make([]string, len(last.Findings))
	for i, f := range last.Findings {
		ids[i] = f.ID
	}
	result.add("audit", Pass, "affected by %s (as of %s)", strings.Join(ids, ", "), last.Vulndb)
}

// reportPlan prints the plan record a release was applied from and checks the
// attached plan is the one it names (PA-12). A release made without a plan has
// nothing to report.
func reportPlan(ctx context.Context, o Options, result *Result, release *releases.Published, m *manifest.Manifest) {
	if m.Plan == nil {
		return
	}
	what := fmt.Sprintf("applied from plan %s (made %s by letsgo %s)",
		short(m.Plan.SHA256), m.Plan.CreatedAt, m.Plan.LetsgoVersion)
	asset, ok := release.Asset(planFileName)
	if !ok {
		result.add("plan", Fail, "%s, but %s is not attached", what, planFileName)
		return
	}
	data, err := o.Client.DownloadAsset(ctx, o.Repo, asset.ID)
	if err != nil {
		result.add("plan", Warn, "%s; could not read %s: %v", what, planFileName, err)
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != m.Plan.SHA256 {
		result.add("plan", Fail, "%s, but the attached %s is %s", what, planFileName, short(got))
		return
	}
	result.add("plan", Pass, "%s", what)
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
