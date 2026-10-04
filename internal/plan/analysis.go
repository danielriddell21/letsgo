package plan

import (
	"context"
	"errors"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// checkVulnerabilities refuses to publish a binary that can reach known
// vulnerable code.
//
// The claim is deliberately narrow. Not "this release has no vulnerable
// dependencies", which is unachievable and would block every release, but
// "nothing in this binary can execute code with a known advisory against it".
// That is checkable, actionable, and rare enough to be worth stopping for.
func (p *Plan) checkVulnerabilities(ctx context.Context, opts Options) {
	if !p.Features.On(feature.Vulncheck) {
		p.add("vulnerabilities", Skip, disabledByConfig)
		return
	}

	found, err := gate.Vulncheck(ctx, p.Global, p.Module.Dir)

	switch {
	case errors.Is(err, gate.ErrToolMissing):
		// A gate that did not run is not a gate that passed.
		p.skip("vulnerabilities", feature.Vulncheck, "%v", err)
		return
	case err != nil:
		p.add("vulnerabilities", Warn, "could not be checked: %v", err)
		return
	case len(found) == 0:
		p.add("vulnerabilities", Pass, "no reachable vulnerabilities")
		return
	}

	lines := make([]string, 0, len(found)+1)
	for _, v := range found {
		lines = append(lines, v.String())
	}

	if opts.AllowVulnerable {
		// Recorded rather than suppressed: the manifest carries gate results,
		// so a consumer can see what this release was published in spite of.
		p.add("vulnerabilities", Warn, "%s\naccepted with --allow-vulnerable", strings.Join(lines, "\n"))
		return
	}

	lines = append(lines, "override with --allow-vulnerable")
	p.add("vulnerabilities", Fail, "%s", strings.Join(lines, "\n"))
}

// apiCompatibility is the check name every step of checkAPICompatibility
// reports under. feature.APIGate is the feature that check is gated and required by.
const (
	apiCompatibility = "api compatibility"
)

// checkAPICompatibility refuses a release whose version promises more
// compatibility than its API delivers.
//
// Within a major version, removing or changing an exported symbol breaks every
// dependant at compile time. Go's answer is a new major version with a new
// import path, and nothing enforces it, so the mistake is made quietly and
// found by other people.
func (p *Plan) checkAPICompatibility(ctx context.Context, opts Options) {
	if !p.Features.On(feature.APIGate) {
		p.add(apiCompatibility, Skip, disabledByConfig)
		return
	}
	// Nothing importable is a fact about the module, not a gap in the release,
	// so it stays a Skip even when the gate is required.
	if importable, err := gate.Importable(ctx, p.GoBin, p.Module.Dir); err == nil && !importable {
		p.add(apiCompatibility, Skip, "%v", gate.ErrNothingExported)
		return
	}
	if p.Tag == "" {
		p.skip(apiCompatibility, feature.APIGate, "not a tagged release")
		return
	}

	tags, err := discover.Tags(ctx, p.GitBin, p.RootDir, p.Scope.Prefix)
	if err != nil {
		p.skip(apiCompatibility, feature.APIGate, "no earlier release to compare against")
		return
	}
	previous, ok := p.Scope.PreviousTag(tags, p.Tag)
	if !ok {
		p.skip(apiCompatibility, feature.APIGate, "no earlier release to compare against")
		return
	}

	old, cleanup, err := discover.CheckoutTag(ctx, p.GitBin, p.RootDir, previous, p.Scope.Dir)
	if err != nil {
		p.add(apiCompatibility, Warn, "could not check out %s: %v", previous, err)
		return
	}
	defer cleanup()

	changes, err := gate.APIDiff(ctx, p.Global, p.GoBin, old, p.Module.Dir)
	switch {
	case errors.Is(err, gate.ErrToolMissing), errors.Is(err, gate.ErrNothingExported):
		p.skip(apiCompatibility, feature.APIGate, "%v", err)
		return
	case err != nil:
		p.add(apiCompatibility, Warn, "could not be checked: %v", err)
		return
	}
	p.APIChanges = changes

	breaking := gate.Incompatibles(changes)
	if len(breaking) == 0 {
		p.add(apiCompatibility, Pass, "the exported API is backward compatible with %s", previous)
		return
	}

	// A major bump is exactly what an incompatible change calls for, so
	// making one is the correct outcome rather than a problem.
	if bumpBetween(previous, p.Tag) == "major" {
		p.add(apiCompatibility, Pass, "%d incompatible change(s), and %s is a major release",
			len(breaking), p.Tag)
		return
	}

	if opts.AllowBreaking {
		p.add(apiCompatibility, Warn, "%s\naccepted with --allow-breaking", describe(breaking))
		return
	}

	p.add(apiCompatibility, Fail,
		"%s is not a major release, but the API is not backward compatible with %s\n%s\n%s",
		p.Tag, previous, describe(breaking),
		"a breaking change needs a major version and a matching /vN module path\noverride with --allow-breaking")
}

func describe(changes []gate.Change) string {
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, "  "+c.String())
	}
	return strings.Join(lines, "\n")
}

// bumpBetween reports how two versions differ.
func bumpBetween(previous, current string) string {
	from, okFrom := semver.Parse(previous)
	to, okTo := semver.Parse(current)
	if !okFrom || !okTo {
		return "unknown"
	}
	switch {
	case to.Major != from.Major:
		return "major"
	case to.Minor != from.Minor:
		return "minor"
	default:
		return "patch"
	}
}
