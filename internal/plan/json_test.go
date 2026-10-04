package plan_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// JSON is doctor's own schema-versioned wire form, reused for every command
// that renders one: the resolved values, gates, artifacts, features and
// plugins an editor needs (ED-7), rather than the human-readable report.
func TestPlanJSON(t *testing.T) {
	p := &plan.Plan{
		Project: "foo",
		Version: "1.2.3",
		Git:     git.State{ShortCommit: "abc1234"},
		Sources: []plan.Provenance{{Field: "project", Value: "foo", From: "go.mod"}},
		Checks:  []plan.Check{{Name: "go.mod", Status: plan.Pass, Detail: "ok"}},
		Artifacts: []plan.Artifact{
			{Name: "foo_1.2.3_linux_amd64.tar.gz"},
		},
		Features: feature.Resolve([]string{"changelog"}),
		Required: []string{"vulncheck"},
		Plugins: map[plugin.Hook]plugin.Plugin{
			plugin.HookLDFlags: {Command: "letsgo-env", Version: "v1.0.0", Digest: "sha256:deadbeef"},
		},
	}

	data, err := p.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	for _, want := range []string{
		`"schema": 1`,
		`"project": "foo"`,
		`"version": "1.2.3"`,
		`"commit": "abc1234"`,
		`"field": "project"`,
		`"name": "go.mod"`,
		`"status": "pass"`,
		`"foo_1.2.3_linux_amd64.tar.gz"`,
		`"disabled": [`,
		`"changelog"`,
		`"required": [`,
		`"vulncheck"`,
		`"ldflags"`,
		`"command": "letsgo-env"`,
		`"digest": "sha256:deadbeef"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON() = %s, want it to contain %q", data, want)
		}
	}
}

func TestPlanJSONOmitsEmptyOptionalFields(t *testing.T) {
	p := &plan.Plan{Project: "foo", Features: feature.Resolve(nil)}

	data, err := p.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	for _, absent := range []string{`"resolved"`, `"artifacts"`, `"disabled"`, `"required"`, `"plugins"`} {
		if strings.Contains(string(data), absent) {
			t.Errorf("JSON() = %s, want no %q field when empty", data, absent)
		}
	}
}
