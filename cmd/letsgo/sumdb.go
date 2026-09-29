package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/sumdb"
)

// checkSumdb cross-checks the just-published release's source archive
// against sum.golang.org and the module proxy (SD-1 through SD-5), unless
// skipped for a private module or a disabled proxy warm (SD-7).
//
// Best effort like warmProxy, except a mismatch is an alarm rather than a
// warning: the release is already public, so this exits non-zero to catch
// attention (in CI, most likely) rather than silently continuing.
func checkSumdb(ctx context.Context, p *plan.Plan, dir string, result *release.Result, proxyWarmDisabled bool) error {
	if proxyWarmDisabled {
		fmt.Println("  · skipped sum.golang.org check: proxy warm is disabled")
		return nil
	}
	if skip, reason := sumdb.PrivateModule(p.Module.Path); skip {
		fmt.Printf("  · skipped sum.golang.org check: private module (%s)\n", reason)
		return nil
	}

	archivePath := filepath.Join(dir, result.Source.Name)

	r, err := sumdb.Check(ctx, sumdb.DefaultURL, p.Proxy, p.Module.Path, p.Version, archivePath)
	if err != nil {
		fmt.Printf("  ! could not check sum.golang.org: %v\n", err)
		return nil
	}

	if r.NotFound {
		fmt.Printf("  ! sum.golang.org has no record for %s@%s yet\n", p.Module.Path, p.Version)
		fmt.Printf("    check again in a few minutes, or manually: curl %s/lookup/%s@%s\n",
			sumdb.DefaultURL, p.Module.Path, p.Version)
		return nil
	}

	if r.Matched {
		fmt.Println("  ✓ sum.golang.org agrees with the source archive")
		return nil
	}

	fmt.Println("  ✗ sum.golang.org does not agree with the source archive")
	if r.SumH1 != r.ZipH1 {
		fmt.Printf("    sumdb reports %s, the module proxy reports %s\n", r.SumH1, r.ZipH1)
	}
	for _, name := range r.Mismatched {
		fmt.Printf("    mismatched: %s\n", name)
	}
	for _, name := range r.Missing {
		fmt.Printf("    missing: %s\n", name)
	}
	fmt.Printf("    consider `letsgo yank %s`\n", p.Tag)
	return fmt.Errorf("sumdb: %s does not match the published source archive", p.Tag)
}
