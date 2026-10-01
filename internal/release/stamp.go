package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/manifest"
)

// PlanFileName is the asset a release applied from a plan carries.
const PlanFileName = "letsgo.plan.json"

// StampPlan records the plan a release was applied from: it writes the plan
// beside the release's files, names its digest in the manifest, and writes the
// manifest and SHA256SUMS again to match.
//
// It runs after the rebuild has been held to the plan, never before: the plan
// predicts the manifest without this record, since a plan cannot contain its
// own digest.
func StampPlan(r *Result, planFile []byte, createdAt, letsgoVersion string) error {
	if err := os.WriteFile(filepath.Join(r.Dir, PlanFileName), planFile, 0o600); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	sum := sha256.Sum256(planFile)
	r.Manifest.Plan = &manifest.PlanRecord{
		SHA256: hex.EncodeToString(sum[:]), CreatedAt: createdAt, LetsgoVersion: letsgoVersion,
	}

	// Before SHA256SUMS, which is last, so that it covers the plan too.
	files := make([]string, 0, len(r.Files)+1)
	for _, name := range r.Files {
		if name == build.ChecksumFile {
			files = append(files, PlanFileName)
		}
		files = append(files, name)
	}
	r.Files = files

	return r.Restamp()
}
