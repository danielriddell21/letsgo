package release

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/manifest"
)

// disable sbom must mean no SBOM asset, not merely one that goes unmentioned.
func TestWriteSBOMSkippedWhenDisabled(t *testing.T) {
	p := &plan.Plan{Features: feature.Resolve([]string{"sbom"})}

	name, sum, err := writeSBOM(p, &manifest.Manifest{}, "0.1.0", t.TempDir())
	if err != nil || name != "" || sum != "" {
		t.Errorf("writeSBOM = (%q, %q, %v), want empty and no error", name, sum, err)
	}
}

// disable install-script must mean no installer, even for a release that
// would otherwise generate one — an artifact, a tag and a GitHub repo, which
// is exactly what makes the ungated function proceed.
func TestWriteInstallerSkippedWhenDisabled(t *testing.T) {
	p := &plan.Plan{
		Features: feature.Resolve([]string{"install-script"}),
		Tag:      "v1.0.0",
		HasRepo:  true,
		Repo:     discover.Repo{Host: "github.com", Owner: "you", Name: "tool"},
	}
	artifacts := []build.Artifact{
		{OS: "linux", Arch: "amd64", Archive: "tool_1.0.0_linux_amd64.tar.gz", ArchiveSHA256: "deadbeef"},
	}

	name, sum, err := writeInstaller(p, artifacts, t.TempDir())
	if err != nil || name != "" || sum != "" {
		t.Errorf("writeInstaller = (%q, %q, %v), want empty and no error", name, sum, err)
	}
}

// A consumer reading the manifest must be able to tell "required" apart from
// "disabled", not just see one merged list.
func TestFeaturesRecordIncludesRequired(t *testing.T) {
	p := &plan.Plan{Required: []string{"vulncheck"}}

	got := featuresRecord(p)
	if got == nil || len(got.Disabled) != 0 || len(got.Required) != 1 || got.Required[0] != "vulncheck" {
		t.Errorf("featuresRecord = %+v", got)
	}
}

func TestFeaturesRecordNilWhenNothingDisabledOrRequired(t *testing.T) {
	p := &plan.Plan{}
	if got := featuresRecord(p); got != nil {
		t.Errorf("featuresRecord = %+v, want nil", got)
	}
}
