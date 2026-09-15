package release

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/stage"
)

// Stage builds one machine's share of a release and describes it, without
// publishing anything.
//
// The difference from Build is what gets written beside the artifacts: a stage
// description rather than a manifest, because a manifest is a claim about a
// whole release and this is a part of one.
func Stage(
	ctx context.Context,
	p *plan.Plan,
	dir, toolVersion string,
	warnf func(string, ...any),
) (*Result, error) {
	if !p.OK() {
		return nil, fmt.Errorf("release: refusing to build a plan that did not pass its gates")
	}
	// Images are assembled from binaries this machine holds and published
	// under one index, which a release split across machines has no single
	// place to assemble. Saying so beats publishing an index with half its
	// platforms.
	if p.Image != nil {
		return nil, fmt.Errorf(
			"release: a staged release cannot publish a container image yet;\n" +
				"  build the image from a release that runs on one machine, or drop the image directive")
	}

	result, err := buildParts(ctx, p, dir, toolVersion, warnf)
	if err != nil {
		return nil, err
	}

	described := &stage.Stage{
		Schema:          manifest.Schema,
		Project:         result.Manifest.Project,
		Version:         result.Manifest.Version,
		Tag:             result.Manifest.Tag,
		Commit:          result.Manifest.Commit,
		SourceDateEpoch: result.Manifest.SourceDateEpoch,
		ModuleDir:       result.Manifest.ModuleDir,
		Matrix:          targetNames(p.Matrix),
		Targets:         builtTargets(result.Artifacts),
		Builder:         result.Manifest.Builder,
		Modules:         result.Manifest.Modules,
		Gates:           result.Manifest.Gates,
		APIChanges:      result.Manifest.APIChanges,
		Artifacts:       result.Manifest.Artifacts,
		Source:          result.Manifest.Source,
	}
	if err := described.Write(dir); err != nil {
		return nil, err
	}

	result.Files = append(archiveNames(result.Artifacts), result.Source.Name, stage.FileName)
	return result, nil
}

// Merge assembles staged directories into a complete release in dir.
//
// The artifacts are copied rather than rebuilt: they were produced on machines
// that could produce them, which is the entire reason the release was split.
func Merge(p *plan.Plan, stages []string, dir, toolVersion string) (*Result, error) {
	merged, err := stage.Merge(stages)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}

	// The plan is the merging machine's, so its own idea of what to build is
	// not the release. What it is good for is the things publishing needs and
	// a stage cannot know: the repository, the tap, the notes.
	if merged.Manifest.Commit != p.Git.Commit {
		return nil, fmt.Errorf(
			"release: the staged artifacts were built from %s, but this checkout is at %s",
			short(merged.Manifest.Commit), short(p.Git.Commit))
	}

	artifacts, err := collect(merged, dir)
	if err != nil {
		return nil, err
	}

	source, err := collectSource(merged, stages, dir)
	if err != nil {
		return nil, err
	}

	files, err := writeMetadata(p, merged.Manifest, toolVersion, dir, artifacts, source)
	if err != nil {
		return nil, err
	}

	return &Result{
		Dir: dir, Manifest: merged.Manifest, Artifacts: artifacts, Source: source, Files: files,
	}, nil
}

// collect copies each staged artifact into the release directory, rebuilding
// the build.Artifact records the installer and the checksum file need.
func collect(merged *stage.Merged, dir string) ([]build.Artifact, error) {
	artifacts := make([]build.Artifact, 0, len(merged.Manifest.Artifacts))

	for _, a := range merged.Manifest.Artifacts {
		from := merged.From[a.Name]
		if err := copyFile(filepath.Join(from, a.Name), filepath.Join(dir, a.Name)); err != nil {
			return nil, err
		}

		binaries := make([]build.Binary, 0, len(a.Executables()))
		for _, b := range a.Executables() {
			binaries = append(binaries, build.Binary{Name: b.Name, Size: b.Size, SHA256: b.SHA256})
		}

		artifacts = append(artifacts, build.Artifact{
			Archive: a.Name, Binaries: binaries, Target: a.OS + "/" + a.Arch,
			OS: a.OS, Arch: a.Arch, Size: a.Size, BinarySize: a.BinarySize,
			LDFlags: a.Build.LDFlags, ArchiveSHA256: a.SHA256,
		})
	}
	return artifacts, nil
}

// collectSource takes the source archive from whichever stage produced it.
// Every stage produces the same bytes — stage.Merge has already refused any
// that did not — so which one is arbitrary.
func collectSource(merged *stage.Merged, stages []string, dir string) (build.Source, error) {
	if merged.Manifest.Source == nil {
		return build.Source{}, fmt.Errorf("release: no staged directory published a source archive")
	}
	name := merged.Manifest.Source.Archive

	for _, from := range stages {
		path := filepath.Join(from, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := copyFile(path, filepath.Join(dir, name)); err != nil {
			return build.Source{}, err
		}
		return build.Source{
			Name: name, SHA256: merged.Manifest.Source.SHA256, Size: info.Size(),
		}, nil
	}
	return build.Source{}, fmt.Errorf("release: %s is named by the stages but present in none of them", name)
}

func copyFile(from, to string) error {
	src, err := os.Open(from) //nolint:gosec // a staged directory the caller named
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("release: copying %s: %w", filepath.Base(from), err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

func targetNames(targets []gobuild.Target) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.String()
	}
	return out
}

// builtTargets are the targets this machine actually produced artifacts for.
func builtTargets(artifacts []build.Artifact) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range artifacts {
		if !seen[a.Target] {
			seen[a.Target] = true
			out = append(out, a.Target)
		}
	}
	sort.Strings(out)
	return out
}

func archiveNames(artifacts []build.Artifact) []string {
	out := make([]string, len(artifacts))
	for i, a := range artifacts {
		out[i] = a.Archive
	}
	return out
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
