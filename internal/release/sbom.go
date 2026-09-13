package release

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/sbom"
)

// writeSBOM generates the dependency document and returns its name and digest.
//
// Written after the manifest is assembled and before it is encoded, because
// the SBOM describes the same artifacts and takes their digests from the same
// place — there is exactly one record of what this release contains, and both
// files are views of it.
func writeSBOM(p *plan.Plan, m *manifest.Manifest, toolVersion, dir string) (string, string, error) {
	repo := ""
	if p.HasRepo {
		repo = p.Repo.Owner + "/" + p.Repo.Name
	}

	document, err := sbom.Generate(sbom.Options{
		Project:    p.Project,
		ModulePath: p.Module.Path,
		Version:    p.Version,
		Commit:     p.Git.Commit,
		Repo:       repo,
		Created:    p.Git.CommitTime,
		Tool:       toolVersion,
		GoVersion:  m.Builder.Go,
		Modules:    m.Modules.List,
		Artifacts:  m.Artifacts,
	})
	if err != nil {
		return "", "", err
	}

	path := filepath.Join(dir, sbom.FileName)
	if err := os.WriteFile(path, document, 0o600); err != nil {
		return "", "", fmt.Errorf("release: writing %s: %w", sbom.FileName, err)
	}

	sum, err := sha256File(path)
	if err != nil {
		return "", "", err
	}
	return sbom.FileName, sum, nil
}
