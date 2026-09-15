package release_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/stage"
)

// twoMachines is a matrix no single runner has to build, so the shares are
// genuinely disjoint rather than the same build done twice.
const twoMachines = "build linux/amd64 linux/arm64\n"

// stageShare builds one machine's share into a fresh directory.
func stageShare(t *testing.T, repo string, targets []string) string {
	t.Helper()

	dir := t.TempDir()
	p := resolve(t, repo, targets)
	if !p.Staged {
		t.Fatalf("plan for %v is not staged", targets)
	}

	result, err := release.Stage(t.Context(), p, dir, "0.1.0", nil)
	if err != nil {
		t.Fatalf("Stage(%v): %v", targets, err)
	}

	// A stage describes a part of a release, so it writes a stage file and
	// not a manifest: a manifest here would be a claim about a whole release
	// that this machine is in no position to make.
	if _, err := os.Stat(filepath.Join(dir, stage.FileName)); err != nil {
		t.Errorf("no stage file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); err == nil {
		t.Error("a stage wrote a manifest")
	}
	for _, name := range result.Files {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is listed but missing: %v", name, err)
		}
	}
	return dir
}

// The merged release must be the one a single machine would have produced:
// every artifact present, carrying the digests the machine that built it
// recorded, under one manifest.
func TestMergeAssemblesTheSharesIntoOneRelease(t *testing.T) {
	repo := fixtureRepo(t, twoMachines)
	first := stageShare(t, repo, []string{"linux/amd64"})
	second := stageShare(t, repo, []string{"linux/arm64"})

	out := t.TempDir()
	result, err := release.Merge(resolve(t, repo, nil), []string{first, second}, out, "0.1.0")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	m := result.Manifest
	if len(m.Artifacts) != 2 {
		t.Fatalf("merged manifest has %d artifacts, want 2", len(m.Artifacts))
	}
	for _, a := range m.Artifacts {
		path := filepath.Join(out, a.Name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s was named but not copied: %v", a.Name, err)
			continue
		}
		// The bytes are the staged ones, not a rebuild: copying them is the
		// entire reason the release was split.
		if info.Size() != a.Size {
			t.Errorf("%s is %d bytes, but the manifest says %d", a.Name, info.Size(), a.Size)
		}
	}

	if result.Source.Name == "" {
		t.Error("the merged release has no source archive")
	}
	if _, err := os.Stat(filepath.Join(out, result.Source.Name)); err != nil {
		t.Errorf("the source archive was not collected: %v", err)
	}
	for _, name := range result.Files {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s is listed but missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, manifest.FileName)); err != nil {
		t.Errorf("the merge wrote no manifest: %v", err)
	}
}

// Half a release published as a whole one is worse than no release: the
// missing platforms look like platforms the project does not support.
func TestMergeRefusesAnIncompleteMatrix(t *testing.T) {
	repo := fixtureRepo(t, twoMachines)
	only := stageShare(t, repo, []string{"linux/amd64"})

	_, err := release.Merge(resolve(t, repo, nil), []string{only}, t.TempDir(), "0.1.0")
	if err == nil {
		t.Fatal("merging one share of a two-machine release succeeded")
	}
	if !strings.Contains(err.Error(), "linux/arm64") {
		t.Errorf("the error does not name what is missing: %v", err)
	}
}

// The merging machine's checkout decides what gets published — the notes, the
// tap, the repository — so artifacts built from another commit would be
// published under claims that do not describe them.
func TestMergeRefusesSharesFromAnotherCommit(t *testing.T) {
	repo := fixtureRepo(t, twoMachines)
	first := stageShare(t, repo, []string{"linux/amd64"})
	second := stageShare(t, repo, []string{"linux/arm64"})

	// A different config, so the commit differs: the fixture is deterministic
	// enough that two identical repositories share a hash.
	elsewhere := fixtureRepo(t, twoMachines+"archive README.md\n")
	_, err := release.Merge(resolve(t, elsewhere, nil), []string{first, second}, t.TempDir(), "0.1.0")
	if err == nil {
		t.Fatal("merging shares built from another commit succeeded")
	}
	if !strings.Contains(err.Error(), "checkout") {
		t.Errorf("the error does not say the commits disagree: %v", err)
	}
}
