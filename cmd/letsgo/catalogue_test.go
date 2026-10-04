package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danielriddell21/letsgo/modsyntax"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/forgerelease"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	installer "github.com/danielriddell21/letsgo/internal/install"
	"github.com/danielriddell21/letsgo/internal/notes"
	"github.com/danielriddell21/letsgo/internal/notes/notestest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/sbom"
	"github.com/danielriddell21/letsgo/manifest"
)

// probe is how one catalogue entry is shown to be consulted: by what the
// release does, not by whether its name appears in the source.
type probe struct {
	// ran reports whether the feature's behaviour happened when the named
	// features are disabled. With nothing disabled it must be true, so a probe
	// that can never observe the feature fails rather than passing vacuously.
	ran func(t *testing.T, disabled []string) bool

	// skipFails puts the feature in a state where it would be skipped and
	// reports whether that became a failure, with the feature required or not.
	skipFails func(t *testing.T, required bool) bool

	// noSkip explains why the feature has no skip to turn into a failure: the
	// only thing that skips it is `disable`, which `require` cannot be
	// combined with.
	noSkip string
}

// A catalogue entry nothing consults is a promise the tool does not keep: the
// directive validates, `letsgo features` lists it, and the release ignores it
// (FT-11). The test walks the catalogue itself, so a new entry is covered the
// moment it is added, and fails until it has a probe here.
//
// Each entry is proven by behaviour. One that can be disabled must do its
// work by default and not when disabled. One that can be required must turn
// its skip into a failure when required. An entry that can be neither is
// always on, and is proven by the config refusing every attempt to switch it.
func TestEveryCatalogueEntryIsConsultedByBehaviour(t *testing.T) {
	// Nothing here may reach a forge, or read a token off the machine.
	for _, name := range []string{"GITHUB_ACTIONS", "GITHUB_TOKEN", "GH_TOKEN", "LETSGO_TAP_TOKEN"} {
		t.Setenv(name, "")
	}
	probes := catalogueProbes()

	for _, f := range feature.All {
		t.Run(string(f.Name), func(t *testing.T) {
			if f.Disable || f.Require {
				p, ok := probes[string(f.Name)]
				if !ok {
					t.Fatalf("%s can be disabled or required, but no probe shows it being consulted", string(f.Name))
				}
				if f.Disable {
					requireDisableChangesBehaviour(t, f, p)
				}
				if f.Require {
					requireRequireChangesBehaviour(t, f, p)
				}
			}
			requireConfigAgrees(t, f)
			if f.Enable != "" {
				requireEnableActs(t, f)
			}
		})
	}
}

// Every probe names a real entry, so a retired feature does not leave a
// probe behind that nothing runs.
func TestEveryProbeNamesACatalogueEntry(t *testing.T) {
	for name := range catalogueProbes() {
		if _, ok := feature.Lookup(name); !ok {
			t.Errorf("probe for %q, which is not in the catalogue", name)
		}
	}
}

func requireDisableChangesBehaviour(t *testing.T, f feature.Feature, p probe) {
	t.Helper()
	if !p.ran(t, nil) {
		t.Errorf("%s: its behaviour was not observed with nothing disabled", string(f.Name))
	}
	if p.ran(t, []string{string(f.Name)}) {
		t.Errorf("%s: its behaviour still happened when disabled", string(f.Name))
	}
}

func requireRequireChangesBehaviour(t *testing.T, f feature.Feature, p probe) {
	t.Helper()
	if p.skipFails == nil {
		if p.noSkip == "" {
			t.Fatalf("%s can be required, but its probe neither turns a skip into a failure nor says why it has none", string(f.Name))
		}
		return
	}
	if p.skipFails(t, false) {
		t.Errorf("%s: a skip was already a failure without `require`", string(f.Name))
	}
	if !p.skipFails(t, true) {
		t.Errorf("%s: `require` did not turn its skip into a failure", string(f.Name))
	}
}

// requireConfigAgrees holds the catalogue's Disable and Require flags to what
// letsgo.mod actually accepts: a directive the catalogue allows decodes, one
// it does not is an error, and naming one feature in both is a contradiction.
func requireConfigAgrees(t *testing.T, f feature.Feature) {
	t.Helper()
	for _, c := range []struct {
		directive string
		allowed   bool
	}{{"disable", f.Disable}, {"require", f.Require}} {
		_, err := decodeConfig(c.directive + " " + string(f.Name) + "\n")
		if c.allowed && err != nil {
			t.Errorf("%s %s: %v", c.directive, string(f.Name), err)
		}
		if !c.allowed && err == nil {
			t.Errorf("%s %s was accepted, but the catalogue does not allow it", c.directive, string(f.Name))
		}
	}
	if f.Disable && f.Require {
		if _, err := decodeConfig("disable " + string(f.Name) + "\nrequire " + string(f.Name) + "\n"); err == nil {
			t.Errorf("%s: disabling and requiring it together was accepted", string(f.Name))
		}
	}
}

func decodeConfig(src string) (*config.Config, error) {
	file, err := modsyntax.Parse("letsgo.mod", []byte(src))
	if err != nil {
		return nil, err
	}
	return config.Decode(file)
}

// enableDirectives are what a repository writes to turn on a feature that is
// off by default, and what that does to the plan.
var enableDirectives = map[string]struct {
	config string
	on     func(*plan.Plan) bool
}{
	"budget": {"budget " + gobuild.Host().String() + " 15MB\n", func(p *plan.Plan) bool { return len(p.Budgets) > 0 }},
	"brew":   {"brew you/tap\n", func(p *plan.Plan) bool { return p.Tap.Name != "" }},
	"image":  {linuxTarget() + "image\n", func(p *plan.Plan) bool { return p.Image != nil }},
}

// linuxTarget adds a linux target when the host is not one. The fixture builds
// for the host alone, and an image or install.sh is only made for unix.
func linuxTarget() string {
	if gobuild.Host().OS == "linux" {
		return ""
	}
	return "build linux/amd64\n"
}

func requireEnableActs(t *testing.T, f feature.Feature) {
	t.Helper()
	if !slices.Contains(config.Directives(), f.Enable) {
		t.Errorf("%s is enabled by %q, which is not a known directive", string(f.Name), f.Enable)
	}
	e, ok := enableDirectives[string(f.Name)]
	if !ok {
		t.Fatalf("%s is enabled by %q, but no probe shows the directive switching it on", string(f.Name), f.Enable)
	}
	if e.on(resolvePlan(t, "", nil, plan.Options{})) {
		t.Errorf("%s is on with no directive", string(f.Name))
	}
	if !e.on(resolvePlan(t, e.config, nil, plan.Options{})) {
		t.Errorf("%q did not turn %s on", strings.TrimSpace(e.config), string(f.Name))
	}
}

// resolvePlan plans the fixture module with config appended to its letsgo.mod.
func resolvePlan(t *testing.T, config string, files map[string]string, opts plan.Options) *plan.Plan {
	t.Helper()
	opts.Dir = moduleFixtureWith(t, config, files)
	p, err := plan.Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return p
}

// directives renders disabled and required feature names as letsgo.mod lines.
func directives(disabled, required []string) string {
	var b strings.Builder
	for _, name := range disabled {
		b.WriteString("disable " + name + "\n")
	}
	for _, name := range required {
		b.WriteString("require " + name + "\n")
	}
	return b.String()
}

func requiredIf(required bool, name string) []string {
	if required {
		return []string{name}
	}
	return nil
}

func catalogueProbes() map[string]probe {
	return map[string]probe{
		"sbom":           {ran: builtFile(sbom.FileName), noSkip: "only `disable sbom` skips it, and that cannot be combined with `require sbom`"},
		"install-script": {ran: builtFile(installer.FileName), skipFails: installScriptSkipFails},
		"changelog":      {ran: changelogRan, noSkip: "only `disable changelog` skips it, and that cannot be combined with `require changelog`"},
		"diff-notes":     {ran: diffNotesRan, skipFails: diffNotesSkipFails},
		"randomart":      {ran: randomartRan, skipFails: randomartSkipFails},
		"proxy-warm":     {ran: proxyWarmRan},
		"sumdb":          {ran: planGateRan("sumdb", plan.Options{Publish: true}), skipFails: sumdbSkipFails},
		"vulncheck":      {ran: planGateRan("vulnerabilities", plan.Options{Analyse: true}), skipFails: vulncheckSkipFails},
		"api-gate": {
			ran:       planGateRan("api compatibility", plan.Options{Analyse: true}, libFile),
			skipFails: planSkipFails("api compatibility", plan.Options{Analyse: true}, "api-gate", libFile),
		},
	}
}

// libFile makes the fixture module importable, so the API gate has an API to
// look at rather than skipping for want of one.
var libFile = map[string]string{"lib/lib.go": "package lib\n"}

// checkOf is the plan's check of that name, and whether there is one.
func checkOf(p *plan.Plan, name string) (plan.Check, bool) {
	for _, c := range p.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return plan.Check{}, false
}

// planGateRan reports whether a plan gate did its job: it is not the Skip that
// says the config switched it off.
func planGateRan(name string, opts plan.Options, files ...map[string]string) func(*testing.T, []string) bool {
	return func(t *testing.T, disabled []string) bool {
		t.Helper()
		t.Setenv("GOPRIVATE", "")
		t.Setenv("GONOSUMDB", "")
		t.Setenv("GONOSUMCHECK", "")
		var extra map[string]string
		if len(files) > 0 {
			extra = files[0]
		}
		c, ok := checkOf(resolvePlan(t, directives(disabled, nil), extra, opts), name)
		return !ok || c.Status != plan.Skip || !strings.Contains(c.Detail, "disabled by config")
	}
}

// planSkipFails reports whether the named check is a Fail once the feature is
// (or is not) required, in a plan where that check would otherwise Skip.
func planSkipFails(check string, opts plan.Options, feat string, files ...map[string]string) func(*testing.T, bool) bool {
	return func(t *testing.T, required bool) bool {
		t.Helper()
		var extra map[string]string
		if len(files) > 0 {
			extra = files[0]
		}
		c, ok := checkOf(resolvePlan(t, directives(nil, requiredIf(required, feat)), extra, opts), check)
		if !ok {
			t.Fatalf("no %q check in the plan", check)
		}
		if !required && c.Status != plan.Skip {
			t.Skipf("%q is %s, not the Skip this probe needs a tool to be missing for", check, c.Status)
		}
		return c.Status == plan.Fail
	}
}

var vulncheckSkipFails = planSkipFails("vulnerabilities", plan.Options{Analyse: true}, "vulncheck")

// sumdb is skipped for a private module, which GOPRIVATE says this one is.
func sumdbSkipFails(t *testing.T, required bool) bool {
	t.Helper()
	t.Setenv("GOPRIVATE", "example.com/*")
	return planSkipFails("sumdb", plan.Options{Publish: true}, "sumdb")(t, required)
}

// An untagged release has no release page to point install.sh at.
func installScriptSkipFails(t *testing.T, required bool) bool {
	t.Helper()
	p := resolvePlan(t, directives(nil, requiredIf(required, "install-script")), nil, plan.Options{Snapshot: true})
	c, ok := checkOf(p, "install script")
	return ok && c.Status == plan.Fail
}

// builtFile reports whether a release built from the fixture publishes a file.
func builtFile(name string) func(*testing.T, []string) bool {
	return func(t *testing.T, disabled []string) bool {
		t.Helper()
		p := resolvePlan(t, linuxTarget()+directives(disabled, nil), nil, plan.Options{})
		result, err := release.Build(context.Background(), release.BuildOptions{Plan: p, Dir: filepath.Join(t.TempDir(), "dist"), ToolVersion: "test"})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return slices.Contains(result.Files, name)
	}
}

// notesOf writes the release notes for the two-release history fixture with
// the named features disabled or required. sum is the manifest's digest, and
// client what the forge answers.
func notesOf(t *testing.T, disabled, required []string, client *github.Client, sum []byte) (string, error) {
	t.Helper()
	p := &plan.Plan{
		GitBin:   "git",
		Features: feature.Resolve(disabled),
		Required: required,
		Module:   discover.Module{Dir: notestest.History(t)},
		Tag:      "v1.1.0",
	}
	current := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.1.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.2"},
	}
	return notes.Release(context.Background(), notes.Source{
		Plan: p, Client: client, Repo: github.Repo{Owner: "you", Name: "demo"}, Manifest: current, ManifestSum: sum,
	})
}

// quietly disables the sections the probe at hand is not about, so they can
// neither reach the network nor be mistaken for the one under test.
func quietly(disabled []string, sections ...string) []string {
	return append(slices.Clone(disabled), sections...)
}

func changelogRan(t *testing.T, disabled []string) bool {
	t.Helper()
	notes, err := notesOf(t, quietly(disabled, "diff-notes", "randomart"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(notes, "second release")
}

func diffNotesRan(t *testing.T, disabled []string) bool {
	t.Helper()
	previous := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.0.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.1"},
	}
	notes, err := notesOf(t, quietly(disabled, "randomart"), nil, notestest.Forge(t, "you/demo", "v1.0.0", previous), nil)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(notes, "What shipped")
}

// A previous release with no manifest leaves nothing to compare.
func diffNotesSkipFails(t *testing.T, required bool) bool {
	t.Helper()
	_, err := notesOf(t, []string{"randomart"}, requiredIf(required, "diff-notes"),
		notestest.Forge(t, "you/demo", "v1.0.0", nil), nil)
	return err != nil
}

func randomartRan(t *testing.T, disabled []string) bool {
	t.Helper()
	notes, err := notesOf(t, quietly(disabled, "diff-notes"), nil, nil, []byte("manifest digest"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(notes, "Manifest fingerprint")
}

// Without a manifest digest there is nothing to draw.
func randomartSkipFails(t *testing.T, required bool) bool {
	t.Helper()
	_, err := notesOf(t, []string{"diff-notes"}, requiredIf(required, "randomart"), nil, nil)
	return err != nil
}

// proxyWarmRan counts what reaches the module proxy while a release publishes.
func proxyWarmRan(t *testing.T, disabled []string) bool {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	p := &plan.Plan{
		GitBin: "git",
		// sumdb is off so that the release reaches nothing but the proxy.
		Features: feature.Resolve(quietly(disabled, "sumdb")),
		Config:   &config.Config{},
		Proxy:    server.URL,
		Module:   discover.Module{Path: "example.com/demo"},
		Version:  "1.2.3",
		Tag:      "v1.2.3",
	}
	dir, err := os.MkdirTemp(t.TempDir(), "dist")
	if err != nil {
		t.Fatal(err)
	}
	recorder := forgerelease.NewRecorder(nil)
	if _, err := publication.Publish(context.Background(), publication.Options{
		Plan: p, Dir: dir, Forge: recorder, Tap: recorder,
		Result: &release.Result{Manifest: &manifest.Manifest{}},
	}); err != nil {
		t.Fatal(err)
	}
	return hits.Load() > 0
}
