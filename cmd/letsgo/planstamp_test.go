package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func stampFixture(t *testing.T) *release.Result {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.tar.gz"), []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &release.Result{
		Dir: dir, Files: []string{"demo.tar.gz", manifest.FileName, build.ChecksumFile},
		Manifest: &manifest.Manifest{Project: "demo"},
	}
}

func writingManifest(op plandiff.Op) []plandiff.Action {
	return []plandiff.Action{{Kind: plandiff.KindAsset, Target: manifest.FileName, Op: op}}
}

func TestStampAppliedLeavesAReleaseWithoutAPlanAlone(t *testing.T) {
	result := stampFixture(t)
	got, err := stampApplied(nil, result)
	if err != nil || got != nil || result.Manifest.Plan != nil {
		t.Errorf("stampApplied(nil) = %v, %v; plan %+v", got, err, result.Manifest.Plan)
	}
}

// A plan that does not write letsgo.json (already published) must not add a
// write it never listed.
func TestStampAppliedLeavesAPlanThatKeepsTheManifestAlone(t *testing.T) {
	result := stampFixture(t)
	file := &plandiff.File{Actions: writingManifest(plandiff.Keep)}

	got, err := stampApplied(file, result)
	if err != nil || got != file || result.Manifest.Plan != nil {
		t.Errorf("stampApplied = %v, %v; plan %+v, want no stamp", got, err, result.Manifest.Plan)
	}
}

// The record names the attached bytes, the plan lists the asset it adds, and
// SHA256SUMS covers it.
func TestStampAppliedRecordsAndAttachesThePlan(t *testing.T) {
	result := stampFixture(t)
	file := &plandiff.File{
		Schema: plandiff.FileSchema, Kind: plandiff.FileKindRelease, Tag: "v1.0.0",
		LetsgoVersion: "0.31.0", CreatedAt: "2026-09-30T10:00:00Z",
		Manifest: []byte(`{}`), Actions: writingManifest(plandiff.Add),
	}

	got, err := stampApplied(file, result)
	if err != nil {
		t.Fatalf("stampApplied: %v", err)
	}

	attached, err := os.ReadFile(filepath.Join(result.Dir, release.PlanFileName))
	if err != nil {
		t.Fatalf("the plan was not written: %v", err)
	}
	sum := sha256.Sum256(attached)
	want := hex.EncodeToString(sum[:])
	if rec := result.Manifest.Plan; rec == nil || rec.SHA256 != want ||
		rec.CreatedAt != file.CreatedAt || rec.LetsgoVersion != file.LetsgoVersion {
		t.Errorf("plan record = %+v, want sha256 %s of the attached file", rec, want)
	}

	last := got.Actions[len(got.Actions)-1]
	if last.Target != release.PlanFileName || last.Op != plandiff.Add || last.Planned != "sha256:"+want {
		t.Errorf("last action = %+v, want the plan asset added", last)
	}
	if len(file.Actions) != 1 {
		t.Errorf("the original plan's actions were changed: %+v", file.Actions)
	}

	sums, err := os.ReadFile(filepath.Join(result.Dir, build.ChecksumFile))
	if err != nil || !strings.Contains(string(sums), want+"  "+release.PlanFileName) {
		t.Errorf("SHA256SUMS = %q (%v), want a line for the plan", sums, err)
	}
	if got := sumsFrom(result)[release.PlanFileName]; got != want {
		t.Errorf("sumsFrom plan = %q, want %q", got, want)
	}
}

func TestStampAppliedReportsAnUnwritableRelease(t *testing.T) {
	result := stampFixture(t)
	result.Dir = filepath.Join(t.TempDir(), "missing")
	file := &plandiff.File{Manifest: []byte(`{}`), Actions: writingManifest(plandiff.Add)}

	if _, err := stampApplied(file, result); err == nil {
		t.Error("stampApplied succeeded with nowhere to write")
	}
}

func TestHoldAndStampRefusesARebuildThatIsNotThePlan(t *testing.T) {
	result := stampFixture(t)
	file := &plandiff.File{Tag: "v1.0.0", Actions: writingManifest(plandiff.Add)}

	if _, err := holdAndStamp(file, resolvedPlan("v2.0.0", "abc"), result); err == nil {
		t.Error("holdAndStamp accepted a rebuild of another tag")
	}
	if result.Manifest.Plan != nil {
		t.Errorf("a refused rebuild was stamped: %+v", result.Manifest.Plan)
	}
}

func TestHoldAndStampStampsARebuildThatMatchesThePlan(t *testing.T) {
	result, digest := rebuiltIn(t, `{"version":"1.3.0"}`)
	result.Manifest = &manifest.Manifest{Project: "demo"}
	result.Files = []string{manifest.FileName, build.ChecksumFile}
	file := planFileFor("v1.3.0", "abc", `{"version":"1.3.0"}`, digest)
	file.Actions = writingManifest(plandiff.Add)

	got, err := holdAndStamp(file, resolvedPlan("v1.3.0", "abc"), result)
	if err != nil || got == nil || result.Manifest.Plan == nil {
		t.Fatalf("holdAndStamp = %v, %v; want the release stamped", got, err)
	}
}
