package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/install"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/manifest"
)

// A promotion stamps promoted_from into the manifest after the build, and an
// applied plan stamps its record. The installer carries the manifest's digest
// and refuses any other, so it has to follow the stamp, or the stable
// release's install.sh rejects the stable release's own letsgo.json.
func TestRestampRewritesTheInstallerForTheNewManifest(t *testing.T) {
	p := &plan.Plan{
		Project:  "tool",
		Version:  "1.0.0",
		Features: feature.Resolve(nil),
		Tag:      "v1.0.0",
		Source:   plan.Source{Location: discover.Location{Repo: discover.Repo{Host: "github.com", Owner: "you", Name: "tool"}}},
	}
	artifacts := []build.Artifact{
		{OS: "linux", Arch: "amd64", Archive: "tool_1.0.0_linux_amd64.tar.gz", ArchiveSHA256: "deadbeef"},
	}

	dir := t.TempDir()
	m := &manifest.Manifest{Schema: 1, Project: "tool", Version: "1.0.0", Tag: "v1.0.0"}
	if err := m.Write(filepath.Join(dir, manifest.FileName)); err != nil {
		t.Fatal(err)
	}
	built, err := sha256File(filepath.Join(dir, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	opts := installerFor(p, artifacts)
	if opts == nil {
		t.Fatal("installerFor gave no installer for a tagged GitHub release with a linux build")
	}
	if _, _, err := renderInstaller(*opts, dir, built); err != nil {
		t.Fatal(err)
	}

	r := &Result{
		Dir: dir, Manifest: m, installer: opts,
		Files: []string{manifest.FileName, install.FileName, build.ChecksumFile},
	}
	m.PromotedFrom = &manifest.PromotedFrom{Tag: "v1.0.0-rc.1", ManifestSHA256: built}
	if err := r.Restamp(); err != nil {
		t.Fatal(err)
	}

	stamped, err := r.Digest(manifest.FileName)
	if err != nil {
		t.Fatal(err)
	}
	if stamped == built {
		t.Fatal("the stamp did not change the manifest, so this proves nothing")
	}
	script, err := os.ReadFile(filepath.Join(dir, install.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "MANIFEST_SHA256='"+stamped+"'") {
		t.Errorf("install.sh does not check the stamped manifest %s", stamped)
	}
	if strings.Contains(string(script), built) {
		t.Errorf("install.sh still names the manifest from before the stamp, %s", built)
	}

	// SHA256SUMS vouches for the installer as it is now, not as it was built.
	scriptSum, err := r.Digest(install.FileName)
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(dir, build.ChecksumFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sums), scriptSum+"  "+install.FileName) {
		t.Errorf("%s = %q, want a line for the rewritten %s", build.ChecksumFile, sums, install.FileName)
	}
}

// A Result with no installer, as a snapshot has, restamps without one.
func TestRestampWithoutAnInstallerWritesNone(t *testing.T) {
	dir := t.TempDir()
	r := &Result{Dir: dir, Manifest: &manifest.Manifest{Schema: 1}, Files: []string{manifest.FileName, build.ChecksumFile}}
	if err := r.Restamp(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, install.FileName)); !os.IsNotExist(err) {
		t.Errorf("Restamp wrote %s for a release that has none (stat err = %v)", install.FileName, err)
	}
}
