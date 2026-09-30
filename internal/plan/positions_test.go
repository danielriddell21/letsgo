package plan_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

const wrongHookPin = "plugin ldflags letsgo-cask v1.0.0 sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

// Every check a directive can fail points at the line that caused it, so an
// editor can underline the directive rather than merely name it.
func TestFailingChecksPointAtTheirDirective(t *testing.T) {
	tests := []struct {
		name  string
		mod   string
		check string
		line  int
	}{
		{"module", "\nmodule nowhere\n", "module", 2},
		{"tags", "build linux/amd64\ntags not!atag\n", "tags", 2},
		{"plugin hook", "project foo\n" + wrongHookPin + "\n", "plugins", 2},
		{"required install script", "\nrequire install-script\n", "install script", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepo(t)
			r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
			r.write("main.go", "package main\n\nfunc main() {}\n")
			r.write("letsgo.mod", tt.mod)
			r.commit("v1.0.0")

			p := r.resolve(plan.Options{})

			c, ok := failing(p, tt.check)
			if !ok {
				t.Fatalf("no failing %q check; got %s", tt.check, checkNames(p))
			}
			want := filepath.Join(r.dir, "letsgo.mod")
			if c.Pos == nil || c.Pos.File != want || c.Pos.Line != tt.line {
				t.Errorf("%s pos = %+v, want %s:%d", tt.check, c.Pos, want, tt.line)
			}
		})
	}
}

func failing(p *plan.Plan, name string) (plan.Check, bool) {
	for _, c := range p.Checks {
		if c.Name == name && c.Status == plan.Fail {
			return c, true
		}
	}
	return plan.Check{}, false
}

func checkNames(p *plan.Plan) string {
	var names []string
	for _, c := range p.Checks {
		names = append(names, fmt.Sprintf("%s:%v", c.Name, c.Status))
	}
	return strings.Join(names, ", ")
}

// A plugin that lives in the checkout can be changed by the checkout, so the
// plan says so; the pin's digest is the only thing keeping it honest.
func TestRelativePluginPathWarns(t *testing.T) {
	digest := strings.Repeat("a", 64)

	for _, tt := range []struct {
		command string
		warns   bool
	}{
		{"./tools/mine", true},
		{"tools/mine", true},
		{"letsgo-mine", false},
		{"/usr/local/bin/letsgo-mine", false},
	} {
		t.Run(tt.command, func(t *testing.T) {
			p := resolveWithMod(t, "build linux/amd64\nplugin ldflags "+tt.command+" v1.0.0 sha256:"+digest+"\n")

			var warned *plan.Check
			for i, c := range p.Checks {
				if c.Name == "plugins" && c.Status == plan.Warn && strings.Contains(c.Detail, "inside the repository") {
					warned = &p.Checks[i]
				}
			}
			if got := warned != nil; got != tt.warns {
				t.Fatalf("warned = %v, want %v; checks: %s", got, tt.warns, checkNames(p))
			}
			if warned != nil && (warned.Pos == nil || warned.Pos.Line != 2) {
				t.Errorf("warning pos = %+v, want line 2", warned.Pos)
			}
		})
	}
}
