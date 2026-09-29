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
	"context"
	"fmt"
	"io"
	"time"

	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/verify"
)

// Schema is the audit.json format version this letsgo writes.
const Schema = 1

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

	// Tag is the release to audit.
	Tag string

	// WorkDir is scratch space for the release's extracted source.
	WorkDir string
}

// Result is what one audit run established.
type Result struct {
	Tag   string
	Entry Entry
}

// Report writes a human-readable summary.
func (r *Result) Report(w io.Writer) {
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
// database.
//
// This is audit's tracer bullet: it fetches the release, verifies its
// source archive against the manifest (reusing verify's own path, so an
// audit and a verify never disagree about what "the release's source"
// means), and runs the same reachability-aware scan the release-time gate
// does. Recording the result on the release, and skipping unchanged runs,
// is a later phase.
func Run(ctx context.Context, o Options) (*Result, error) {
	release, err := o.Client.ReleaseByTag(ctx, o.Repo, o.Tag)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("audit: %s has no release tagged %s", o.Repo, o.Tag)
	}

	m, err := verify.FetchManifest(ctx, o.Client, o.Repo, release)
	if err != nil {
		return nil, err
	}

	source, err := verify.SourceFromArchive(ctx, o.Client, o.Repo, release, m, o.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}

	report, err := gate.VulncheckReport(ctx, source)
	if err != nil {
		return nil, err
	}

	entry := Entry{
		At:          time.Now().UTC(),
		Vulndb:      report.VulndbDate,
		Govulncheck: report.GovulncheckVersion,
		Status:      Clean,
	}
	for _, v := range report.Vulnerabilities {
		entry.Status = Affected
		entry.Findings = append(entry.Findings, Finding{ID: v.ID, Module: v.Module, Fixed: v.FixedIn})
	}

	return &Result{Tag: release.TagName, Entry: entry}, nil
}
