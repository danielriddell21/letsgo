package plan_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

// caskPin pins letsgo-cask to the tap-files hook, the way letsgo.mod would.
// tap-files never runs during `plan` itself (only a real release invokes
// it), so the plan resolves cleanly without the plugin being installed —
// exactly what these tests need, since they are about the config file check,
// not about running the plugin.
const caskPin = "build linux/amd64\nplugin tap-files letsgo-cask v0.1.0 sha256:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"

func pluginRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", caskPin)
	return r
}

// A plugin with no config file of its own, in either location, is not
// something the plan has anything to say about.
func TestPluginConfigFilesSilentWhenNeitherExists(t *testing.T) {
	r := pluginRepo(t)
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	for _, c := range p.Checks {
		if c.Name == "plugins" {
			t.Errorf("unexpected plugins check: %+v", c)
		}
	}
}

// A legacy root config file still works, but the plan says to move it.
func TestPluginConfigFilesWarnsOnLegacyOnly(t *testing.T) {
	r := pluginRepo(t)
	r.write("letsgo-cask.mod", "token-command echo hi\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if !p.OK() {
		t.Fatalf("a legacy-only config file should only warn: %+v", p.Checks)
	}
	c := check(t, p, "plugins")
	if c.Status != plan.Warn || !strings.Contains(c.Detail, "letsgo-cask.mod") ||
		!strings.Contains(c.Detail, ".letsgo") {
		t.Errorf("check = %+v", c)
	}
}

// The new .letsgo/<name>.mod location alone needs no warning at all.
func TestPluginConfigFilesSilentOnNewLocationOnly(t *testing.T) {
	r := pluginRepo(t)
	r.write(".letsgo/cask.mod", "token-command echo hi\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	for _, c := range p.Checks {
		if c.Name == "plugins" {
			t.Errorf("unexpected plugins check: %+v", c)
		}
	}
}

// Both files at once is ambiguous — core cannot tell which one the plugin
// would actually read — so the release must not proceed.
func TestPluginConfigFilesFailsWhenBothExist(t *testing.T) {
	r := pluginRepo(t)
	r.write("letsgo-cask.mod", "token-command echo hi\n")
	r.write(".letsgo/cask.mod", "token-command echo hi\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if p.OK() {
		t.Fatal("both a legacy and a new config file should fail the plan")
	}
	c := check(t, p, "plugins")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "letsgo-cask.mod") ||
		!strings.Contains(c.Detail, ".letsgo") {
		t.Errorf("check = %+v", c)
	}
}
