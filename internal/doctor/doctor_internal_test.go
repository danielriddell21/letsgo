package doctor

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

func TestVersionSatisfies(t *testing.T) {
	cases := map[string]struct {
		want  string
		exact bool
		got   string
		ok    bool
	}{
		"toolchain exact match":                     {"go1.24.7", true, "go1.24.7", true},
		"toolchain patch mismatch":                  {"go1.24.7", true, "go1.24.8", false},
		"toolchain newer local still switches":      {"go1.24.7", true, "go1.25.0", false},
		"go directive satisfied by newer patch":     {"go1.24", false, "go1.24.7", true},
		"go directive satisfied by newer minor":     {"go1.24", false, "go1.27.1", true},
		"go directive satisfied by exact match":     {"go1.24.7", false, "go1.24.7", true},
		"go directive not satisfied by older":       {"go1.28", false, "go1.27.1", false},
		"go directive not satisfied by older patch": {"go1.24.7", false, "go1.24.6", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := versionSatisfies(tc.want, tc.exact, tc.got); got != tc.ok {
				t.Errorf("versionSatisfies(%q, %v, %q) = %v, want %v", tc.want, tc.exact, tc.got, got, tc.ok)
			}
		})
	}
}

func TestCheckGateToolReportsAMissingToolAsWarn(t *testing.T) {
	r := &Result{}
	r.checkGateTool("letsgo-doctor-test-nonexistent-tool", "install it somehow", false)

	if len(r.Checks) != 1 {
		t.Fatalf("Checks = %d, want 1", len(r.Checks))
	}
	c := r.Checks[0]
	if c.Status != Warn {
		t.Errorf("Status = %v, want Warn", c.Status)
	}
	if c.Hint != "install it somehow" {
		t.Errorf("Hint = %q, want the install command", c.Hint)
	}
	if !r.OK() {
		t.Error("OK() = false, want true: a Warn alone must not fail the run")
	}
}

func TestCheckGateToolEscalatesToFailWhenRequired(t *testing.T) {
	r := &Result{}
	r.checkGateTool("letsgo-doctor-test-nonexistent-tool", "install it somehow", true)

	if r.Checks[0].Status != Fail {
		t.Errorf("Status = %v, want Fail", r.Checks[0].Status)
	}
	if r.OK() {
		t.Error("OK() = true, want false with a Fail check present")
	}
}

func TestRequiresVulncheck(t *testing.T) {
	t.Run("no letsgo.mod", func(t *testing.T) {
		if requiresVulncheck(&config.Config{}) {
			t.Error("want false with no letsgo.mod")
		}
	})

	t.Run("require vulncheck", func(t *testing.T) {
		if !requiresVulncheck(&config.Config{Required: []string{"vulncheck"}}) {
			t.Error("want true")
		}
	})

	t.Run("require something else", func(t *testing.T) {
		if requiresVulncheck(&config.Config{Required: []string{"api-gate"}}) {
			t.Error("want false")
		}
	})
}
