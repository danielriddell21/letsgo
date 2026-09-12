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

	// Images are assembled but not pushed. Their digests are already fixed,
	// which is why the manifest can name them.
	Images []ImageBuild

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

	// Cached only when the commit fully describes the source. A dirty
	// worktree has inputs no key can capture, so a hit would be a build
	// nobody could account for.
	cacheKey := ""
	if p.Git.Clean {
		cacheKey = p.Git.Commit
	}
	cache := build.OpenCache("")

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
			CacheKey:     cacheKey,
			Cache:        cache,
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

	if err := checkBudgets(artifacts, p.Budgets, warnf); err != nil {
		return nil, err
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

	// Assembled here, published later. An image's digest is a pure function of
	// its inputs, so it is known before anything reaches a registry — which is
	// the only order in which the manifest can record it.
	images, err := buildImages(ctx, p, artifacts, warnf)
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
		APIChanges:      apiChanges(p),
		Images:          imageRecords(images),
	}

	for _, a := range artifacts {
		m.Artifacts = append(m.Artifacts, manifest.Artifact{
			Name: a.Archive, OS: a.OS, Arch: a.Arch, Size: a.Size, BinarySize: a.BinarySize,
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

	installer, installerSum, err := writeInstaller(p, artifacts, dir)
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
	if installer != "" {
		sums = append(sums, build.Sum{Name: installer, SHA256: installerSum})
	}
	if _, err := build.WriteChecksums(dir, sums); err != nil {
		return nil, err
	}

	files := make([]string, 0, len(sums)+1)
	for _, a := range artifacts {
		files = append(files, a.Archive)
	}
	files = append(files, source.Name, manifest.FileName)
	if installer != "" {
		files = append(files, installer)
	}
	files = append(files, build.ChecksumFile)

	return &Result{
		Dir: dir, Manifest: m, Artifacts: artifacts, Source: source,
		Images: images, Files: files,
	}, nil
}

// apiChanges carries the exported API delta into the manifest, so comparing
// two releases later needs nothing but the two files.
func apiChanges(p *plan.Plan) []manifest.APIChange {
	if len(p.APIChanges) == 0 {
		return nil
	}
	out := make([]manifest.APIChange, 0, len(p.APIChanges))
	for _, c := range p.APIChanges {
		out = append(out, manifest.APIChange{
			Kind: string(c.Kind), Package: c.Package, Text: c.Text,
		})
	}
	return out
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
