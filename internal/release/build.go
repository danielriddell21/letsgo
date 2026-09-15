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
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
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
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}

	goVersion, err := gobuild.Version(ctx, "")
	if err != nil {
		return nil, err
	}

	artifacts, err := buildCommands(ctx, p, dir, warnf)
	if err != nil {
		return nil, err
	}

	if err := checkBudgets(artifacts, p.Budgets, warnf); err != nil {
		return nil, err
	}

	source, err := build.WriteSource(ctx, build.SourceOptions{
		ModuleDir: p.RootDir,
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

	m := describe(p, toolVersion, goVersion, source, mods, artifacts, images)

	files, err := writeMetadata(p, m, toolVersion, dir, artifacts, source)
	if err != nil {
		return nil, err
	}

	return &Result{
		Dir: dir, Manifest: m, Artifacts: artifacts, Source: source,
		Images: images, Files: files,
	}, nil
}

// writeMetadata writes the manifest, the installer and the checksum file, and
// returns everything to publish in upload order.
func writeMetadata(
	p *plan.Plan,
	m *manifest.Manifest,
	toolVersion, dir string,
	artifacts []build.Artifact,
	source build.Source,
) ([]string, error) {
	// Generated before the manifest is written, so the manifest can name it
	// and SHA256SUMS can cover both.
	document, documentSum, err := writeSBOM(p, m, toolVersion, dir)
	if err != nil {
		return nil, err
	}
	m.SBOM = document

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
	sums = append(sums, build.Sum{Name: document, SHA256: documentSum})
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
	files = append(files, source.Name, manifest.FileName, document)
	if installer != "" {
		files = append(files, installer)
	}
	return append(files, build.ChecksumFile), nil
}

// describe assembles the release manifest: everything a consumer needs to
// rebuild this release and compare, in one file.
func describe(
	p *plan.Plan,
	toolVersion, goVersion string,
	source build.Source,
	mods manifest.Modules,
	artifacts []build.Artifact,
	images []ImageBuild,
) *manifest.Manifest {
	m := &manifest.Manifest{
		Schema:          manifest.Schema,
		Project:         p.Project,
		Version:         p.Version,
		Tag:             p.Tag,
		Commit:          p.Git.Commit,
		SourceDateEpoch: p.Git.CommitTime.Unix(),
		ModuleDir:       p.Config.ModuleDir,
		Builder:         builder(p, toolVersion, goVersion),
		Source:          &manifest.Source{Archive: source.Name, SHA256: source.SHA256},
		Modules:         mods,
		Gates:           gates(p),
		APIChanges:      apiChanges(p),
		Images:          imageRecords(images),
		Artifacts:       make([]manifest.Artifact, 0, len(artifacts)),
	}

	for _, a := range artifacts {
		record := manifest.Artifact{
			Name: a.Archive, OS: a.OS, Arch: a.Arch,
			Size: a.Size, BinarySize: a.BinarySize,
			SHA256: a.ArchiveSHA256,
			Build: manifest.Build{
				Flags:   buildFlags(p),
				LDFlags: a.LDFlags,
				Env:     buildEnv(p, a),
			},
		}

		// One binary keeps the long-standing spelling, so a reader written
		// against an earlier release still understands the common case; only
		// an archive holding several needs the per-binary list.
		if len(a.Binaries) == 1 {
			record.Binary = a.Binaries[0].Name
			record.BinarySHA256 = a.Binaries[0].SHA256
		} else {
			for _, b := range a.Binaries {
				record.Binaries = append(record.Binaries,
					manifest.Binary{Name: b.Name, Size: b.Size, SHA256: b.SHA256})
			}
		}

		m.Artifacts = append(m.Artifacts, record)
	}
	manifest.SortArtifacts(m.Artifacts)

	return m
}

// buildEnv is the environment an artifact was compiled under, as recorded.
func buildEnv(p *plan.Plan, a build.Artifact) map[string]string {
	env := map[string]string{"CGO_ENABLED": "0", "GOOS": a.OS, "GOARCH": a.Arch}
	if p.CGo.Path != "" {
		env["CGO_ENABLED"] = "1"
	}
	return env
}

// builder records what produced the release, including any plugin that took
// part in deciding it.
func builder(p *plan.Plan, toolVersion, goVersion string) manifest.Builder {
	b := manifest.Builder{Tool: "letsgo " + toolVersion, Go: goVersion}

	if p.CGo.Path != "" {
		b.CC = &manifest.CCompiler{
			Name: "zig", Version: p.CGo.Version, Digest: p.CGo.Digest,
			Host: gobuild.Host().String(),
		}
	}

	hooks := make([]string, 0, len(p.Plugins))
	for hook := range p.Plugins {
		hooks = append(hooks, string(hook))
	}
	sort.Strings(hooks)

	for _, hook := range hooks {
		used := p.Plugins[plugin.Hook(hook)]
		b.Plugins = append(b.Plugins, manifest.BuilderPlugin{
			Hook: hook, Command: used.Command, Version: used.Version, Digest: used.Digest,
		})
	}
	return b
}

// buildFlags are the go build flags an artifact was produced with, recorded so
// that verification replays them rather than reconstructing them.
func buildFlags(p *plan.Plan) []string {
	flags := []string{"-trimpath", "-buildvcs=false"}
	if len(p.Tags) > 0 {
		flags = append(flags, "-tags="+strings.Join(p.Tags, ","))
	}
	return flags
}

// buildCommands compiles and packages every command in the plan.
func buildCommands(ctx context.Context, p *plan.Plan, dir string, warnf func(string, ...any)) ([]build.Artifact, error) {
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
	for _, group := range p.Groups {
		commands := make([]build.Command, len(group.Commands))
		for i, cmd := range group.Commands {
			commands[i] = build.Command{Package: cmd.RelPath, Binary: cmd.BinaryName}
		}
		// A single-command group is named after the project, not the command,
		// so the binary inside it follows the archive.
		if len(commands) == 1 {
			commands[0].Binary = group.Name
		}

		produced, err := build.Run(ctx, build.Options{
			ModuleDir:    p.Module.Dir,
			FilesDir:     p.RootDir,
			Commands:     commands,
			Name:         group.Name,
			Version:      p.Version,
			Commit:       p.Git.ShortCommit,
			ModTime:      p.Git.CommitTime,
			Targets:      p.Targets,
			ExtraFiles:   p.Files,
			ExtraLDFlags: p.LDFlags,
			Tags:         p.Tags,
			CGo:          p.CGo,
			Symbols: build.VersionSymbols{
				Version: p.Symbols.Version, Commit: p.Symbols.Commit, Date: p.Symbols.Date,
			},
			WorkDir:  dir,
			Smoke:    smoke,
			Warnf:    warnf,
			CacheKey: cacheKey,
			Cache:    cache,
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
	return artifacts, nil
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
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("release: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
