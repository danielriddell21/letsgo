package plan_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

// A known plugin already documents its hook, so pinning it to a different
// one is not a config that happens to disagree with letsgo — it is wrong,
// and the release should say so before anything is built.
func TestKnownPluginPinnedToTheWrongHookFails(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+
		"plugin ldflags letsgo-multi v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if p.OK() {
		t.Fatal("letsgo-multi answers archive-layout, not ldflags")
	}
	c := check(t, p, "plugins")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "archive-layout") {
		t.Errorf("check = %+v", c)
	}
}

// letsgo-cask answers no hook at all; pinning it to one is exactly as wrong
// as pinning it to the wrong one.
func TestStandaloneKnownPluginCannotBePinned(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+
		"plugin ldflags letsgo-cask v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if p.OK() {
		t.Fatal("letsgo-cask does not answer a hook")
	}
	c := check(t, p, "plugins")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "does not answer a hook") {
		t.Errorf("check = %+v", c)
	}
}

// A repository building several commands without letsgo-multi is not wrong,
// but it is exactly the shape the plugin exists for.
func TestManyCommandsHintAtMulti(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/tools\n\ngo 1.24\n")
	r.write("cmd/alpha/main.go", "package main\n\nfunc main() {}\n")
	r.write("cmd/beta/main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if !hasWarning(p, "letsgo-multi") {
		t.Errorf("no hint toward letsgo-multi: %+v", p.Checks)
	}
}

// Pinning letsgo-multi already is the answer to that hint, so it should stop
// repeating itself.
func TestManyCommandsWithMultiAlreadyPinnedHasNoHint(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/tools\n\ngo 1.24\n")
	r.write("cmd/alpha/main.go", "package main\n\nfunc main() {}\n")
	r.write("cmd/beta/main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if hasWarning(p, "letsgo-multi groups") {
		t.Errorf("should not hint at a plugin that is already pinned: %+v", p.Checks)
	}
}

// A darwin variant alongside a Homebrew tap is exactly what letsgo-cask
// exists for.
func TestDarwinVariantWithTapHintsAtCask(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+
		"brew you/tap\n"+
		"variant gui (\n\tbuild darwin/arm64\n)\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if !hasWarning(p, "letsgo-cask") {
		t.Errorf("no hint toward letsgo-cask: %+v", p.Checks)
	}
}

// Without a tap, there is nowhere for a cask to be published, so no hint.
func TestDarwinVariantWithoutTapHasNoCaskHint(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+
		"variant gui (\n\tbuild darwin/arm64\n)\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if hasWarning(p, "letsgo-cask") {
		t.Errorf("should not hint without a tap: %+v", p.Checks)
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
