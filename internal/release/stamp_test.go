package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/manifest"
)

func stampResult(t *testing.T) *Result {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.tar.gz"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Result{
		Dir: dir, Files: []string{"a.tar.gz", manifest.FileName, build.ChecksumFile},
		Manifest: &manifest.Manifest{Project: "demo"},
	}
}

func TestStampPlanAttachesAndChecksumsThePlan(t *testing.T) {
	r := stampResult(t)
	if err := StampPlan(r, []byte(`{}`), "2026-09-30T10:00:00Z", "0.31.0"); err != nil {
		t.Fatalf("StampPlan: %v", err)
	}

	if want := []string{"a.tar.gz", manifest.FileName, PlanFileName, build.ChecksumFile}; strings.Join(r.Files, ",") != strings.Join(want, ",") {
		t.Errorf("files = %v, want %v", r.Files, want)
	}
	sums, err := os.ReadFile(filepath.Join(r.Dir, build.ChecksumFile))
	if err != nil || !strings.Contains(string(sums), "  "+PlanFileName) || !strings.Contains(string(sums), "  "+manifest.FileName) {
		t.Errorf("SHA256SUMS = %q (%v), want the plan and manifest covered", sums, err)
	}
	if r.Manifest.Plan == nil || r.Manifest.Plan.LetsgoVersion != "0.31.0" {
		t.Errorf("plan record = %+v", r.Manifest.Plan)
	}
}

func TestStampPlanReportsAFileItCannotHash(t *testing.T) {
	r := stampResult(t)
	r.Files = append([]string{"gone.tar.gz"}, r.Files...)
	if err := StampPlan(r, []byte(`{}`), "", ""); err == nil {
		t.Error("StampPlan succeeded with a missing release file")
	}
}

func TestStampPlanReportsAnUnwritableRelease(t *testing.T) {
	r := stampResult(t)
	r.Dir = filepath.Join(r.Dir, "missing")
	if err := StampPlan(r, []byte(`{}`), "", ""); err == nil {
		t.Error("StampPlan succeeded with nowhere to write")
	}
}
