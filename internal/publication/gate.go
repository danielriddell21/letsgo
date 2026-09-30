package publication

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/sumdb"
)

// sumdbURL is the checksum database consulted; a variable so tests can point
// it at a fake.
var sumdbURL = sumdb.DefaultURL

// gate primes the module proxy, then cross-checks sum.golang.org and the proxy
// against the built source archive — on every non-snapshot, non-draft,
// non-scoped release, before any asset is attached. The tag is already pushed,
// which is all the proxy needs.
func gate(ctx context.Context, out io.Writer, o Options) error {
	p := o.Plan
	if o.Snapshot || p.Config.Draft || p.Config.ModuleDir != "" {
		return nil
	}
	if p.Features.On("proxy-warm") {
		warmProxy(ctx, out, p)
	}
	return checkSumdb(ctx, out, p, o.Dir, o.Result)
}

// warmProxy primes the resolved module proxy so `go install` works
// immediately.
//
// Best effort: a proxy that is slow has not broken a release that is not yet
// public.
func warmProxy(ctx context.Context, out io.Writer, p *plan.Plan) {
	if err := publish.WarmProxy(ctx, p.Proxy, p.Module.Path, p.Version); err != nil {
		fmt.Fprintf(out, "  ! could not prime the module proxy: %v\n", err)
		fmt.Fprintf(out, "    `go install` may fail briefly until the proxy fetches %s\n", p.Tag)
		return
	}
	fmt.Fprintf(out, "  primed %s\n", p.Proxy)
}

// checkSumdb cross-checks the release's source archive against
// sum.golang.org and the module proxy (SD-1 through SD-5), unless skipped for
// a private module, a disabled proxy warm (SD-7) or `disable sumdb`.
//
// A gate: a mismatch fails the release before any asset is attached. Not
// being able to check is only a warning, unless `require sumdb` says a
// release nobody could check must not go out.
func checkSumdb(ctx context.Context, out io.Writer, p *plan.Plan, dir string, result *release.Result) error {
	if !p.Features.On("sumdb") {
		fmt.Fprintln(out, "  · skipped sum.golang.org check: disabled by config")
		return nil
	}
	if !p.Features.On("proxy-warm") {
		fmt.Fprintln(out, "  · skipped sum.golang.org check: proxy warm is disabled")
		return nil
	}
	required := slices.Contains(p.Required, "sumdb")
	if skip, reason := sumdb.PrivateModule(p.Module.Path); skip {
		fmt.Fprintf(out, "  · skipped sum.golang.org check: private module (%s)\n", reason)
		return nil
	}

	archivePath := filepath.Join(dir, result.Source.Name)

	r, err := sumdb.Check(ctx, sumdbURL, p.Proxy, p.Module.Path, p.Version, archivePath)
	if err != nil {
		if required {
			return fmt.Errorf("sumdb: could not check sum.golang.org: %w", err)
		}
		fmt.Fprintf(out, "  ! could not check sum.golang.org: %v\n", err)
		return nil
	}

	return reportSumdb(out, p, r, required)
}

// reportSumdb says what the cross-check found, and returns an error when the
// release must not go out: a mismatch always, a missing record only when
// sumdb is required.
func reportSumdb(out io.Writer, p *plan.Plan, r sumdb.Result, required bool) error {
	if r.NotFound {
		fmt.Fprintf(out, "  ! sum.golang.org has no record for %s@%s yet\n", p.Module.Path, p.Version)
		fmt.Fprintf(out, "    check again in a few minutes, or manually: curl %s/lookup/%s@%s\n",
			sumdbURL, p.Module.Path, p.Version)
		if required {
			return fmt.Errorf("sumdb: no record for %s yet", p.Tag)
		}
		return nil
	}

	if r.Matched {
		fmt.Fprintln(out, "  ✓ sum.golang.org agrees with the source archive")
		return nil
	}

	fmt.Fprintln(out, "  ✗ sum.golang.org does not agree with the source archive")
	if r.SumH1 != r.ZipH1 {
		fmt.Fprintf(out, "    sumdb reports %s, the module proxy reports %s\n", r.SumH1, r.ZipH1)
	}
	for _, name := range r.Mismatched {
		fmt.Fprintf(out, "    mismatched: %s\n", name)
	}
	for _, name := range r.Missing {
		fmt.Fprintf(out, "    missing: %s\n", name)
	}
	fmt.Fprintln(out, "    nothing was published; the tag is pushed and the proxy holds it")
	return fmt.Errorf("sumdb: %s does not match the built source archive", p.Tag)
}
