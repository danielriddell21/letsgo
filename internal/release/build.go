// Package release turns a plan into a complete set of publishable files.
//
// It sits between planning, which decides what a release should contain, and
// publishing, which puts it somewhere. Both `letsgo build` and `letsgo
// release` run exactly this, so what you inspect locally is what gets
// uploaded rather than something assembled a second way.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// Result is everything a release consists of on disk.
type Result struct {
	Dir       string
	Manifest  *manifest.Manifest
	Artifacts []build.Artifact
	Source    build.Source

	// Files are the names of every file to publish, in upload order.
	Files []string
}

// Build produces every artifact described by the plan, plus the source
// archive, the manifest and the checksum file.
func Build(ctx context.Context, p *plan.Plan, dir string, toolVersion string, warnf func(string, ...any)) (*Result, error) {
	if !p.OK() {
		return nil, fmt.Errorf("release: refusing to build a plan that did not pass its gates")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}

	goVersion, err := gobuild.Version(ctx, "")
	if err != nil {
		return nil, err
	}

	// The version must actually reach the binary, and the only way to know
	// that is to run it. Checking the symbol exists proves the linker had
	// somewhere to write, not that the value survived to main.
	smoke := &build.Smoke{Want: p.Version}

	var artifacts []build.Artifact
	for _, cmd := range p.Commands {
		name := p.Project
		if len(p.Commands) > 1 {
			name = cmd.BinaryName
		}

		produced, err := build.Run(ctx, build.Options{
			ModuleDir:    p.Module.Dir,
			Package:      cmd.RelPath,
			Name:         name,
			Version:      p.Version,
			Commit:       p.Git.ShortCommit,
			ModTime:      p.Git.CommitTime,
			Targets:      p.Targets,
			ExtraFiles:   p.Files,
			ExtraLDFlags: p.LDFlags,
			WorkDir:      dir,
			Smoke:        smoke,
			Warnf:        warnf,
		})
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, produced...)

		// Only the first command needs proving; the others share a toolchain
		// and a set of flags, and running each would add time without adding
		// information.
		smoke = nil
	}

	source, err := build.WriteSource(ctx, build.SourceOptions{
		ModuleDir: p.Module.Dir,
		Name:      p.Project,
		Version:   p.Version,
		ModTime:   p.Git.CommitTime,
		WorkDir:   dir,
	})
	if err != nil {
		return nil, err
	}

	mods, err := manifest.SummariseModules(filepath.Join(p.Module.Dir, "go.sum"))
	if err != nil {
		return nil, err
	}

	m := &manifest.Manifest{
		Schema:          manifest.Schema,
		Project:         p.Project,
		Version:         p.Version,
		Tag:             p.Tag,
		Commit:          p.Git.Commit,
		SourceDateEpoch: p.Git.CommitTime.Unix(),
		Builder:         manifest.Builder{Tool: "letsgo " + toolVersion, Go: goVersion},
		Source:          &manifest.Source{Archive: source.Name, SHA256: source.SHA256},
		Modules:         mods,
		Gates:           gates(p),
	}

	for _, a := range artifacts {
		m.Artifacts = append(m.Artifacts, manifest.Artifact{
			Name: a.Archive, OS: a.OS, Arch: a.Arch, Size: a.Size,
			SHA256: a.ArchiveSHA256, BinarySHA256: a.BinarySHA256,
			Build: manifest.Build{
				Flags:   []string{"-trimpath", "-buildvcs=false"},
				LDFlags: a.LDFlags,
				Env:     map[string]string{"CGO_ENABLED": "0", "GOOS": a.OS, "GOARCH": a.Arch},
			},
		})
	}
	manifest.SortArtifacts(m.Artifacts)

	manifestPath := filepath.Join(dir, manifest.FileName)
	if err := m.Write(manifestPath); err != nil {
		return nil, err
	}
	manifestSum, err := sha256File(manifestPath)
	if err != nil {
		return nil, err
	}

	// The checksum file covers everything published except itself, the
	// manifest included: a consumer who trusts SHA256SUMS can then trust the
	// manifest, and through it every digest the manifest records.
	sums := append(build.SumsFor(artifacts),
		build.Sum{Name: source.Name, SHA256: source.SHA256},
		build.Sum{Name: manifest.FileName, SHA256: manifestSum},
	)
	if _, err := build.WriteChecksums(dir, sums); err != nil {
		return nil, err
	}

	files := make([]string, 0, len(sums)+1)
	for _, a := range artifacts {
		files = append(files, a.Archive)
	}
	files = append(files, source.Name, manifest.FileName, build.ChecksumFile)

	return &Result{Dir: dir, Manifest: m, Artifacts: artifacts, Source: source, Files: files}, nil
}

// gates records what the release was checked against, so a consumer can see
// which guarantees a given release actually carries.
func gates(p *plan.Plan) map[string]string {
	out := map[string]string{}
	for _, c := range p.Checks {
		out[c.Name] = string(c.Status)
	}
	return out
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("release: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("release: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
