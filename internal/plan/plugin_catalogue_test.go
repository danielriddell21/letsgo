package plan_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// withStandalonePlugin temporarily adds a known plugin that answers no hook,
// so the check that refuses to pin one can be exercised even though nothing
// in the real catalogue is like this today.
func withStandalonePlugin(t *testing.T, command string) {
	t.Helper()
	orig := plugin.Known
	plugin.Known = append(append([]plugin.KnownPlugin{}, orig...), plugin.KnownPlugin{Command: command})
	t.Cleanup(func() { plugin.Known = orig })
}

// resolveWithMod builds a module with the given commands (default: one
// main.go) and letsgo.mod content, and resolves it.
func resolveWithMod(t *testing.T, mod string, commands ...string) *plan.Plan {
	t.Helper()
	if len(commands) == 0 {
		commands = []string{"main.go"}
	}

	r := newRepo(t)
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	for _, name := range commands {
		r.write(name, "package main\n\nfunc main() {}\n")
	}
	if mod != "" {
		r.write("letsgo.mod", mod)
	}
	r.commit("v1.0.0")
	return r.resolve(plan.Options{})
}

// A known plugin already documents its hook, so pinning it to a different one
// — or to none at all, for one that answers no hook — is wrong, and the
// release should say so before anything is built.
func TestKnownPluginPinnedWrong(t *testing.T) {
	for _, tc := range []struct {
		name, pin, want string
	}{
		{
			"the wrong hook",
			"plugin ldflags letsgo-multi v0.1.0 sha256:" + strings.Repeat("a", 64),
			"archive-layout",
		},
		{
			"a different wrong hook",
			"plugin ldflags letsgo-cask v0.1.0 sha256:" + strings.Repeat("a", 64),
			"tap-files",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := resolveWithMod(t, "build linux/amd64\n"+tc.pin+"\n")
			if p.OK() {
				t.Fatal("a wrongly pinned known plugin should fail the plan")
			}
			c := check(t, p, "plugins")
			if c.Status != plan.Fail || !strings.Contains(c.Detail, tc.want) {
				t.Errorf("check = %+v", c)
			}
		})
	}
}

// A known plugin can also document that it answers no hook at all — nothing
// in the catalogue does today, but the plan still has to refuse pinning one.
func TestKnownPluginWithNoHookCannotBePinned(t *testing.T) {
	withStandalonePlugin(t, "letsgo-standalone")

	p := resolveWithMod(t, "build linux/amd64\n"+
		"plugin ldflags letsgo-standalone v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")
	if p.OK() {
		t.Fatal("a plugin pinned to a hook it does not answer should fail the plan")
	}
	c := check(t, p, "plugins")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "does not answer a hook") {
		t.Errorf("check = %+v", c)
	}
}

// A release's shape can match a first-party plugin's whole job — several
// commands, or a darwin variant beside a Homebrew tap — without pinning one.
// That is not wrong, just more work than it needs to be, so it is a hint
// rather than a failure, and one that stops once the plugin is pinned.
func TestPluginHints(t *testing.T) {
	multiCommands := []string{"cmd/alpha/main.go", "cmd/beta/main.go"}
	multiPin := "plugin archive-layout letsgo-multi v0.1.0 sha256:" + strings.Repeat("a", 64) + "\n"
	darwinVariant := "variant gui (\n\tbuild darwin/arm64\n)\n"

	for _, tc := range []struct {
		name     string
		mod      string
		commands []string
		want     string
		hinted   bool
	}{
		{"many commands hint at multi", "", multiCommands, "letsgo-multi", true},
		{
			"multi already pinned has no hint",
			"build linux/amd64\n" + multiPin, multiCommands, "letsgo-multi groups", false,
		},
		{"darwin variant with a tap hints at cask", "build linux/amd64\nbrew you/tap\n" + darwinVariant, nil, "letsgo-cask", true},
		{"darwin variant without a tap has no hint", "build linux/amd64\n" + darwinVariant, nil, "letsgo-cask", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := resolveWithMod(t, tc.mod, tc.commands...)
			if got := hasWarning(p, tc.want); got != tc.hinted {
				t.Errorf("hasWarning(%q) = %v, checks: %+v", tc.want, got, p.Checks)
			}
		})
	}
}

func hasWarning(p *plan.Plan, substr string) bool {
	for _, c := range p.Checks {
		if c.Name == "plugins" && c.Status == plan.Warn && strings.Contains(c.Detail, substr) {
			return true
		}
	}
	return false
}
