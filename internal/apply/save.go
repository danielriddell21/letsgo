package apply

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/danielriddell21/letsgo/internal/plan"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// Diff is what reading the forge against a fresh build came to.
type Diff struct {
	Actions []plandiff.Action

	// Manifest is the manifest the build produced, and ManifestSHA256 its
	// digest: what an apply must reproduce.
	Manifest       []byte
	ManifestSHA256 string
}

// Save writes what a diff found as a release plan file, made by letsgo
// version, and returns its digest.
func Save(p *plan.Plan, d *Diff, path, version string) (string, error) {
	file := &plandiff.File{
		Schema:         plandiff.FileSchema,
		LetsgoVersion:  version,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		Kind:           plandiff.FileKindRelease,
		Repo:           p.Repo.Owner + "/" + p.Repo.Name,
		Tag:            p.Tag,
		Commit:         p.Git.Commit,
		ManifestSHA256: d.ManifestSHA256,
		Manifest:       json.RawMessage(d.Manifest),
		Actions:        d.Actions,
	}
	return Write(file, path)
}

// Write writes a plan file and returns its digest.
func Write(file *plandiff.File, path string) (string, error) {
	if err := file.Write(path); err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	digest, err := file.Digest()
	if err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	return digest, nil
}
