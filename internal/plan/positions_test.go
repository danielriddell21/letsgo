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

			var found bool
			for _, c := range p.Checks {
				if c.Name != tt.check || c.Status != plan.Fail {
					continue
				}
				found = true
				want := filepath.Join(r.dir, "letsgo.mod")
				if c.Pos == nil || c.Pos.File != want || c.Pos.Line != tt.line {
					t.Errorf("%s pos = %+v, want %s:%d", tt.check, c.Pos, want, tt.line)
				}
			}
			if !found {
				var names []string
				for _, c := range p.Checks {
					names = append(names, fmt.Sprintf("%s:%v", c.Name, c.Status))
				}
				t.Fatalf("no failing %q check; got %s", tt.check, strings.Join(names, ", "))
			}
		})
	}
}
