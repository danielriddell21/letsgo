package plan_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

func minimalRepo(t *testing.T, letsgoMod string) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	if letsgoMod != "" {
		r.write("letsgo.mod", letsgoMod)
	}
	r.commit("v1.0.0")
	return r
}

// A default release has nothing to report: the whole point of the field is
// that it stays quiet until something is actually disabled.
func TestFeaturesSilentByDefault(t *testing.T) {
	r := minimalRepo(t, "")
	p := r.resolve(plan.Options{})

	for _, c := range p.Checks {
		if c.Name == "features" {
			t.Fatalf("unexpected features check: %+v", c)
		}
	}
	if !p.Features.On("vulncheck") || !p.Features.On("sbom") || !p.Features.On("proxy-warm") {
		t.Errorf("Features = %v, want everything on", p.Features)
	}
}

// `disable` in letsgo.mod is what a release actually turns off, not merely a
// name recorded somewhere.
func TestDisableTurnsOffVulncheckAndAPIGate(t *testing.T) {
	r := minimalRepo(t, "disable vulncheck\ndisable api-gate\n")
	p := r.resolve(plan.Options{Analyse: true})

	if got := check(t, p, "vulnerabilities"); got.Status != plan.Skip || !strings.Contains(got.Detail, "disabled") {
		t.Errorf("vulnerabilities = %+v, want a Skip naming the config", got)
	}
	if got := check(t, p, "api compatibility"); got.Status != plan.Skip || !strings.Contains(got.Detail, "disabled") {
		t.Errorf("api compatibility = %+v, want a Skip naming the config", got)
	}

	f := check(t, p, "features")
	if f.Status != plan.Pass {
		t.Errorf("features = %+v, want Pass", f)
	}
	if !strings.Contains(f.Detail, "api-gate") || !strings.Contains(f.Detail, "vulncheck") {
		t.Errorf("features detail = %q, want both names", f.Detail)
	}
}

// --no-proxy-warm means the same thing as `disable proxy-warm` for this run,
// recorded the same way rather than as a second, invisible switch.
func TestNoProxyWarmActsAsADisable(t *testing.T) {
	r := minimalRepo(t, "")
	p := r.resolve(plan.Options{DisableProxyWarm: true})

	if p.Features.On("proxy-warm") {
		t.Error("proxy-warm is still on")
	}
	f := check(t, p, "features")
	if !strings.Contains(f.Detail, "proxy-warm") {
		t.Errorf("features detail = %q, want proxy-warm", f.Detail)
	}

	var source string
	for _, s := range p.Sources {
		if s.Field == "features" {
			source = s.From
		}
	}
	if source != "--no-proxy-warm" {
		t.Errorf("features source = %q, want --no-proxy-warm", source)
	}
}

// A config disable and the one-run flag can name the same or different
// features; either way the release sees one merged answer.
func TestNoProxyWarmCombinesWithConfigDisable(t *testing.T) {
	r := minimalRepo(t, "disable sbom\n")
	p := r.resolve(plan.Options{DisableProxyWarm: true})

	if p.Features.On("sbom") || p.Features.On("proxy-warm") {
		t.Errorf("Features = %v, want both sbom and proxy-warm off", p.Features)
	}

	var source string
	for _, s := range p.Sources {
		if s.Field == "features" {
			source = s.From
		}
	}
	if !strings.Contains(source, "letsgo.mod") || !strings.Contains(source, "--no-proxy-warm") {
		t.Errorf("features source = %q, want both origins named", source)
	}
}

// `require` turns a Skip into a Fail: neither govulncheck nor apidiff is on
// PATH in this environment (internal/gate's own tests rely on the same
// thing), so both gates Skip by default and Fail once required.
func TestRequireTurnsSkipsIntoFails(t *testing.T) {
	r := minimalRepo(t, "require vulncheck\nrequire api-gate\n")
	p := r.resolve(plan.Options{Analyse: true})

	if got := check(t, p, "vulnerabilities"); got.Status != plan.Fail {
		t.Errorf("vulnerabilities = %+v, want Fail", got)
	}
	if got := check(t, p, "api compatibility"); got.Status != plan.Fail {
		t.Errorf("api compatibility = %+v, want Fail", got)
	}

	f := check(t, p, "features")
	if !strings.Contains(f.Detail, "required: api-gate, vulncheck") {
		t.Errorf("features detail = %q, want both names under required", f.Detail)
	}
}

// Without `require`, the same missing tools are unremarkable: a Skip, not a
// Fail.
func TestWithoutRequireToolsAreSkippedNotFailed(t *testing.T) {
	r := minimalRepo(t, "")
	p := r.resolve(plan.Options{Analyse: true})

	if got := check(t, p, "vulnerabilities"); got.Status != plan.Skip {
		t.Errorf("vulnerabilities = %+v, want Skip", got)
	}
	if got := check(t, p, "api compatibility"); got.Status != plan.Skip {
		t.Errorf("api compatibility = %+v, want Skip", got)
	}
}

// require install-script makes its otherwise-silent absence a Fail, at plan
// time rather than after a release has already shipped without one.
func TestRequireInstallScriptFailsWithoutAGitHubRepo(t *testing.T) {
	r := minimalRepo(t, "require install-script\n")
	p := r.resolve(plan.Options{})

	if got := check(t, p, "install script"); got.Status != plan.Fail {
		t.Errorf("install script = %+v, want Fail", got)
	}
}

func TestRequireInstallScriptPassesForAnOrdinaryGitHubRelease(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	r.write("letsgo.mod", "require install-script\n")
	r.git("remote", "add", "origin", "https://github.com/you/foo.git")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if got := check(t, p, "install script"); got.Status != plan.Pass {
		t.Errorf("install script = %+v, want Pass", got)
	}
}

// Without require, install.sh's absence is not a check at all: the plan has
// nothing to say about an output nobody asked to make mandatory.
func TestInstallScriptHasNoCheckWithoutRequire(t *testing.T) {
	r := minimalRepo(t, "")
	p := r.resolve(plan.Options{})

	for _, c := range p.Checks {
		if c.Name == "install script" {
			t.Fatalf("unexpected install script check: %+v", c)
		}
	}
}
