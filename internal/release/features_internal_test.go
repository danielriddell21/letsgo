package release

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/pgpwords"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/randomart"
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
		Source:   plan.Source{Location: discover.Location{Repo: discover.Repo{Host: "github.com", Owner: "you", Name: "tool"}}},
	}
	artifacts := []build.Artifact{
		{OS: "linux", Arch: "amd64", Archive: "tool_1.0.0_linux_amd64.tar.gz", ArchiveSHA256: "deadbeef"},
	}

	name, sum, err := writeInstaller(p, artifacts, t.TempDir(), "")
	if err != nil || name != "" || sum != "" {
		t.Errorf("writeInstaller = (%q, %q, %v), want empty and no error", name, sum, err)
	}
}

// The installer's fingerprint is the release page's: the same randomart and
// the same words, drawn from the same manifest digest (ADR-0014). disable
// randomart drops it here as it does there, but the manifest is still checked.
func TestWriteInstallerCarriesTheManifestFingerprint(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	raw, _ := hex.DecodeString(sum)
	art := randomart.Render(raw, randomart.Title("v1.0.0"))
	words := pgpwords.Rows(pgpwords.Encode(raw))

	for _, tc := range []struct {
		name     string
		disabled []string
		want     bool
	}{
		{"on by default", nil, true},
		{"disable randomart", []string{"randomart"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &plan.Plan{
				Project:  "tool",
				Version:  "1.0.0",
				Features: feature.Resolve(tc.disabled),
				Tag:      "v1.0.0",
				Source:   plan.Source{Location: discover.Location{Repo: discover.Repo{Host: "github.com", Owner: "you", Name: "tool"}}},
			}
			artifacts := []build.Artifact{
				{OS: "linux", Arch: "amd64", Archive: "tool_1.0.0_linux_amd64.tar.gz", ArchiveSHA256: "deadbeef"},
			}

			dir := t.TempDir()
			name, _, err := writeInstaller(p, artifacts, dir, sum)
			if err != nil || name == "" {
				t.Fatalf("writeInstaller = (%q, %v)", name, err)
			}
			script, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(script), "MANIFEST_SHA256='"+sum+"'") {
				t.Error("the installer does not check the manifest")
			}
			for _, part := range []string{art, words} {
				if got := strings.Contains(string(script), part); got != tc.want {
					t.Errorf("fingerprint present = %v, want %v:\n%s", got, tc.want, part)
				}
			}
		})
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
