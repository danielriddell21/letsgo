package verify

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
)

// reportFeatures is exercised directly here for the required-features branch,
// which needs a release built with `require` — and every feature that
// supports it Fails plan.OK() in an environment without its tool on PATH
// (see plan.TestRequireTurnsSkipsIntoFails), so it can't be driven through a
// real release.Build the way the disabled branch is in verify_test.go.
func TestReportFeaturesIncludesRequired(t *testing.T) {
	result := &Result{}
	reportFeatures(result, &manifest.Manifest{
		Features: &manifest.Features{Disabled: []string{"sbom"}, Required: []string{"api-gate"}},
	})

	if len(result.Checks) != 1 {
		t.Fatalf("Checks = %+v, want exactly one", result.Checks)
	}
	c := result.Checks[0]
	if c.Name != "features" || c.Status != Pass {
		t.Errorf("check = %+v", c)
	}
	if c.Detail != "disabled: sbom; required: api-gate" {
		t.Errorf("Detail = %q", c.Detail)
	}
}
