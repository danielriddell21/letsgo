// Package audit re-checks a shipped release's source against today's
// vulnerability database and records what it finds.
//
// The vulnerability gate (internal/gate) runs once, at release time. A
// vulnerability disclosed afterwards is invisible to a release that already
// shipped: the manifest still says the gate passed. audit re-runs the same
// check against the release's own source archive, tying the result to what
// was actually published rather than to what a later commit contains.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/releases/githubsource"
	"github.com/danielriddell21/letsgo/internal/verify"
)

// Schema is the audit.json format version this letsgo writes.
const Schema = 1

// FileName is the asset audit history is kept in, attached to the release
// alongside its other published files. It is not part of the manifest or
// SHA256SUMS (ADR-0012): it is written after the release, by a later audit.
const FileName = "audit.json"

// Status is the outcome of one audit run.
type Status string

const (
	Clean    Status = "clean"
	Affected Status = "affected"
)

// Finding is one reachable vulnerability, as recorded on the release.
type Finding struct {
	ID     string `json:"id"`
	Module string `json:"module"`
	Fixed  string `json:"fixed,omitempty"`
}

// Entry is one audit run, appended to a release's history.
type Entry struct {
	At          time.Time `json:"at"`
	Vulndb      string    `json:"vulndb,omitempty"`
	Govulncheck string    `json:"govulncheck,omitempty"`
	Status      Status    `json:"status"`
	Findings    []Finding `json:"findings,omitempty"`
}

// Record is the full history kept in a release's audit.json.
type Record struct {
	Schema int     `json:"schema"`
	Tag    string  `json:"tag"`
	Audits []Entry `json:"audits"`
}

// Options describe one audit run.
type Options struct {
	Client *github.Client
	Repo   github.Repo

	// Global is the machine config, which may name where govulncheck is.
	// Nil means it names nothing.
	Global *config.Global

	// Tag is the release to audit. Empty means every supported major
	// (RunAll): see Options.Prefix.
	Tag string

	// Prefix scopes RunAll to one monorepo module's tags, the same way
	// discover.Scope.Prefix does. Empty for a root module.
	Prefix string

	// WorkDir is scratch space for the release's extracted source.
	WorkDir string
}

// Result is what one audit run established.
type Result struct {
	Tag   string
	Entry Entry

	// Recorded is false when this run's entry matched the last recorded one
	// (same vulndb date and findings) and so was not appended.
	Recorded bool

	// Skipped says why the release was not audited at all, or is empty.
	Skipped string
}

// Report writes a human-readable summary.
func (r *Result) Report(w io.Writer) {
	if r.Skipped != "" {
		fmt.Fprintf(w, "%s: skipped, %s\n", r.Tag, r.Skipped)
		return
	}
	if r.Entry.Status != Affected {
		fmt.Fprintf(w, "%s: clean (as of %s)\n", r.Tag, r.Entry.Vulndb)
		return
	}
	fmt.Fprintf(w, "%s: affected (as of %s)\n", r.Tag, r.Entry.Vulndb)
	for _, f := range r.Entry.Findings {
		fmt.Fprintf(w, "  · %s in %s", f.ID, f.Module)
		if f.Fixed != "" {
			fmt.Fprintf(w, ", fixed in %s", f.Fixed)
		}
		fmt.Fprintln(w)
	}
}

// Run re-checks one release's source against today's vulnerability
// database and records the result on the release.
//
// It fetches the release, verifies its source archive against the manifest
// (reusing verify's own path, so an audit and a verify never disagree about
// what "the release's source" means), and runs the same reachability-aware
// scan the release-time gate does, under each build configuration the
// manifest records. The new entry is appended to the
// release's audit.json unless it exactly matches the last recorded entry
// (same vulndb date and findings), so a re-run against an unchanged
// database costs nothing to repeat.
func Run(ctx context.Context, o Options) (*Result, error) {
	release, err := releases.ByTag(ctx, o.source(), o.Tag)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("audit: %s has no release tagged %s", o.Repo, o.Tag)
	}
	return auditRelease(ctx, o, release)
}

func (o Options) source() *githubsource.Source {
	return &githubsource.Source{Client: o.Client, Repo: o.Repo}
}

// RunAll audits the newest non-retracted stable release of each major
// version in scope (AU-1): a supported line is checked once, without the
// noise of every patch release on it. Drafts, retracted releases,
// prereleases and out-of-scope tags are skipped. A release with no
// manifest or source archive (predating letsgo) fails the whole run rather
// than being skipped with a reason — that edge case is out of scope for
// this phase.
func RunAll(ctx context.Context, o Options) ([]*Result, error) {
	perMajor, err := releases.PerMajor(ctx, o.source(), discover.Scope{Prefix: o.Prefix})
	if err != nil {
		return nil, err
	}

	results := make([]*Result, 0, len(perMajor))
	for _, release := range perMajor {
		result, err := auditRelease(ctx, o, release)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// auditRelease is the per-release work Run and RunAll share.
//
// Each release gets its own extraction directory under o.WorkDir: RunAll
// calls this once per major version against the same Options, and
// SourceFromArchive rejects a directory that already holds another
// release's extracted archive.
func auditRelease(ctx context.Context, o Options, release *releases.Published) (*Result, error) {
	// An immutable release cannot have audit.json attached or replaced, and
	// an audit that cannot be recorded is not worth running.
	if release.Immutable {
		return &Result{Tag: release.Tag, Skipped: "the release is immutable, so audit.json cannot be recorded on it"}, nil
	}

	m, _, err := releases.Manifest(ctx, o.source(), release)
	if releases.IsNoManifest(err) {
		return nil, fmt.Errorf(
			"verify: release %s has no %s, so there is nothing describing what it should contain\n"+
				"  only releases published by letsgo can be verified",
			release.Tag, manifest.FileName)
	}
	if err != nil {
		return nil, err
	}

	sourceDir, err := os.MkdirTemp(o.WorkDir, "source-")
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	defer func() { _ = os.RemoveAll(sourceDir) }()

	source, err := verify.SourceFromArchive(ctx, o.Client, o.Repo, release, m, sourceDir)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}

	report, err := gate.VulncheckReport(ctx, o.Global, source, scansFor(m)...)
	if err != nil {
		return nil, err
	}

	entry := Entry{
		At:          time.Now().UTC().Truncate(time.Second),
		Vulndb:      report.VulndbDate,
		Govulncheck: report.GovulncheckVersion,
		Status:      Clean,
	}
	for _, v := range report.Vulnerabilities {
		entry.Status = Affected
		entry.Findings = append(entry.Findings, Finding{ID: v.ID, Module: v.Module, Fixed: v.FixedIn})
	}

	record, err := loadRecord(ctx, o, release)
	if err != nil {
		return nil, err
	}

	result := &Result{Tag: release.Tag, Entry: entry}
	if !shouldAppend(record, entry) {
		return result, nil
	}
	record.Audits = append(record.Audits, entry)
	if err := saveRecord(ctx, o, release, record); err != nil {
		return nil, err
	}
	result.Recorded = true
	return result, nil
}

// scansFor is the build configurations the release shipped, one per distinct
// set of build tags and target (AU-4): reachability depends on which files
// compile, so the source is scanned as each archive was built rather than as
// the host would build it. A manifest that records no artifacts yields no
// scans, which leaves govulncheck's own defaults.
func scansFor(m *manifest.Manifest) []gate.Scan {
	var scans []gate.Scan
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		var tags []string
		for _, flag := range a.Build.Flags {
			if rest, ok := strings.CutPrefix(flag, "-tags="); ok && rest != "" {
				tags = strings.Split(rest, ",")
			}
		}
		var env []string
		for _, key := range []string{"CGO_ENABLED", "GOARCH", "GOOS"} {
			if v, ok := a.Build.Env[key]; ok {
				env = append(env, key+"="+v)
			}
		}

		key := strings.Join(tags, ",") + "|" + strings.Join(env, ",")
		if seen[key] {
			continue
		}
		seen[key] = true
		scans = append(scans, gate.Scan{Tags: tags, Env: env})
	}
	return scans
}

// loadRecord fetches a release's existing audit.json, or starts a fresh
// one when the release has never been audited before.
func loadRecord(ctx context.Context, o Options, release *releases.Published) (*Record, error) {
	asset, ok := release.Asset(FileName)
	if !ok {
		return &Record{Schema: Schema, Tag: release.Tag}, nil
	}
	data, err := o.Client.DownloadAsset(ctx, o.Repo, asset.ID)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("audit: release %s has a %s that does not parse: %w", release.Tag, FileName, err)
	}
	return &record, nil
}

// shouldAppend reports whether entry is new information: a release's first
// audit always is, and a later one is only when the vulndb it ran against
// or what it found has changed since the last recorded run (AU-6).
func shouldAppend(record *Record, entry Entry) bool {
	if len(record.Audits) == 0 {
		return true
	}
	last := record.Audits[len(record.Audits)-1]
	if last.Vulndb != entry.Vulndb {
		return true
	}
	return !equalFindings(last.Findings, entry.Findings)
}

func equalFindings(a, b []Finding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// saveRecord writes a release's audit.json, replacing any existing one.
// There is no "update asset content" API, so this follows the same
// delete-then-upload replace pattern internal/publish uses (AU-8: nothing
// else about the release is touched).
func saveRecord(ctx context.Context, o Options, release *releases.Published, record *Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	data = append(data, '\n')

	if existing, ok := release.Asset(FileName); ok {
		if err := o.Client.DeleteAsset(ctx, o.Repo, existing.ID); err != nil {
			return fmt.Errorf("audit: %w", err)
		}
	}
	_, err = o.Client.UploadAsset(ctx, o.Repo, release.ID, FileName, int64(len(data)), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}
