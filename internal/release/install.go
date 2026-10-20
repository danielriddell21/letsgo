package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/install"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/manifest"
	pub "github.com/danielriddell21/letsgo/plugin"
)

// writeInstaller generates the self-verifying installer and returns its name
// and digest, or an empty name when the release has nowhere to point it.
//
// A snapshot has no tag and therefore no download URL, and a repository with
// no forge has no release page; in both cases the honest thing is to publish
// no installer rather than one whose URLs resolve to nothing.
//
// The manifest is already written, so the script carries its digest: it
// checks letsgo.json like an archive, and with randomart on it ends by
// showing the fingerprint the release page shows.
func writeInstaller(p *plan.Plan, artifacts []build.Artifact, dir, manifestSum string) (string, string, error) {
	if !p.Features.On(feature.InstallScript) {
		return "", "", nil
	}
	if p.Tag == "" || !p.HasRepo() || p.Repo.Host != "github.com" {
		return "", "", nil
	}

	targets := make([]install.Target, 0, len(artifacts))
	for _, a := range artifacts {
		targets = append(targets, install.Target{
			OS: a.OS, Arch: a.Arch, Archive: a.Archive, SHA256: a.ArchiveSHA256,
		})
	}
	if len(install.Platforms(targets)) == 0 {
		return "", "", nil
	}

	script, err := install.Script(install.Options{
		Project: p.Project,
		Version: p.Version,
		Tag:     p.Tag,
		BaseURL: strings.TrimSuffix(
			pub.DownloadURL(p.Repo.Owner+"/"+p.Repo.Name, p.Tag, ""), "/"),
		Targets:     targets,
		Manifest:    install.File{Name: manifest.FileName, SHA256: manifestSum},
		Fingerprint: p.Features.On(feature.Randomart),
	})
	if err != nil {
		return "", "", err
	}

	path := filepath.Join(dir, install.FileName)
	// Written executable on purpose: the point of the script is that someone
	// can run it, and the release attaches it as-is.
	if err := os.WriteFile(path, script, 0o700); err != nil { //nolint:gosec // an installer must be runnable
		return "", "", fmt.Errorf("release: writing %s: %w", install.FileName, err)
	}

	sum, err := sha256File(path)
	if err != nil {
		return "", "", err
	}
	return install.FileName, sum, nil
}
