// Package stage carries a release that one machine cannot finish alone.
//
// Most releases do not need this. One does when some artifact can only be
// produced on a particular host — a cgo build for macOS needs Apple's
// frameworks, so it needs a Mac — and the rest of the matrix is built
// elsewhere. Each machine stages what it can; one of them merges the lot and
// publishes once.
//
// The merge is where the guarantees live. Every stage records the commit it
// built, the whole matrix the release is supposed to cover, and the targets it
// covered itself. Merging checks that the stages agree about the first two and
// that between them they account for the third, so a release assembled from
// pieces cannot quietly ship fewer platforms than it promised — which is the
// failure this design exists to avoid, not to introduce.
package stage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/manifest"
)

// FileName is what a staged release is described by, beside its artifacts.
//
// Deliberately not letsgo.json: a stage is not a release, and a file that
// looked like one would eventually be published as one.
const FileName = "letsgo.stage.json"

// Stage is one machine's share of a release.
type Stage struct {
	Schema  int    `json:"schema"`
	Project string `json:"project"`
	Version string `json:"version"`
	Tag     string `json:"tag,omitempty"`
	Commit  string `json:"commit"`

	SourceDateEpoch int64  `json:"source_date_epoch"`
	ModuleDir       string `json:"module_dir,omitempty"`

	// Matrix is every target the release covers, and Targets those this stage
	// produced. Merging compares the union of the second against the first.
	Matrix  []string `json:"matrix"`
	Targets []string `json:"targets"`

	Builder    manifest.Builder     `json:"builder"`
	Modules    manifest.Modules     `json:"modules"`
	Gates      map[string]string    `json:"gates,omitempty"`
	APIChanges []manifest.APIChange `json:"api_changes,omitempty"`

	Artifacts []manifest.Artifact `json:"artifacts"`

	// Source is the source archive, which every stage produces identically
	// because it is a function of the commit. Merging keeps one and proves the
	// others match.
	Source *manifest.Source `json:"source,omitempty"`

	// SBOM is the dependency document's filename, likewise identical
	// everywhere it is produced.
	SBOM string `json:"sbom,omitempty"`
}

// Write saves a stage description into dir.
func (s *Stage) Write(dir string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("stage: encoding: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("stage: writing %s: %w", FileName, err)
	}
	return nil
}

// Read loads the stage description in dir.
func Read(dir string) (*Stage, error) {
	path := filepath.Join(dir, FileName)

	data, err := os.ReadFile(path) //nolint:gosec // a directory the caller named
	if err != nil {
		return nil, fmt.Errorf("stage: reading %s: %w", path, err)
	}

	var s Stage
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("stage: parsing %s: %w", path, err)
	}
	if s.Schema != manifest.Schema {
		return nil, fmt.Errorf("stage: %s has schema %d, which this letsgo does not understand",
			path, s.Schema)
	}
	return &s, nil
}

// Merged is the result of combining stages: a complete manifest, and the
// directories each artifact has to be collected from.
type Merged struct {
	Manifest *manifest.Manifest

	// From maps each artifact's filename to the staged directory holding it.
	From map[string]string
}

// Merge combines staged releases into one manifest.
//
// dirs are the staged directories, in no particular order; the merged
// artifacts are sorted, so the result does not depend on which machine
// finished first.
func Merge(dirs []string) (*Merged, error) {
	if len(dirs) == 0 {
		return nil, fmt.Errorf("stage: no staged directories to merge")
	}

	stages := make([]*Stage, 0, len(dirs))
	for _, dir := range dirs {
		s, err := Read(dir)
		if err != nil {
			return nil, err
		}
		stages = append(stages, s)
	}

	first := stages[0]
	if err := agree(dirs, stages); err != nil {
		return nil, err
	}

	covered, err := coverage(dirs, stages)
	if err != nil {
		return nil, err
	}
	if err := complete(first.Matrix, covered); err != nil {
		return nil, err
	}

	m := &manifest.Manifest{
		Schema:          manifest.Schema,
		Project:         first.Project,
		Version:         first.Version,
		Tag:             first.Tag,
		Commit:          first.Commit,
		SourceDateEpoch: first.SourceDateEpoch,
		ModuleDir:       first.ModuleDir,
		Builder:         first.Builder,
		Source:          first.Source,
		Modules:         first.Modules,
		Gates:           first.Gates,
		APIChanges:      first.APIChanges,
		SBOM:            first.SBOM,
	}

	from := map[string]string{}
	for i, s := range stages {
		for _, a := range s.Artifacts {
			if where, seen := from[a.Name]; seen {
				return nil, fmt.Errorf(
					"stage: %s was produced by both %s and %s; two machines built the same artifact",
					a.Name, where, dirs[i])
			}
			from[a.Name] = dirs[i]
			m.Artifacts = append(m.Artifacts, a)
		}

		// A C toolchain is recorded per release, and only some stages use one.
		if m.Builder.CC == nil && s.Builder.CC != nil {
			m.Builder.CC = s.Builder.CC
		}
	}
	manifest.SortArtifacts(m.Artifacts)

	return &Merged{Manifest: m, From: from}, nil
}

// agree checks that the stages describe the same release.
//
// Every field here is derived from the commit, so disagreement means the
// machines built different source — the one mistake that would otherwise
// produce a release whose parts do not belong together.
func agree(dirs []string, stages []*Stage) error {
	first := stages[0]

	for i, s := range stages[1:] {
		where := dirs[i+1]
		for _, c := range []struct{ field, a, b string }{
			{"commit", first.Commit, s.Commit},
			{"version", first.Version, s.Version},
			{"tag", first.Tag, s.Tag},
			{"project", first.Project, s.Project},
			{"module", first.ModuleDir, s.ModuleDir},
			{"go toolchain", first.Builder.Go, s.Builder.Go},
		} {
			if c.a != c.b {
				return fmt.Errorf("stage: %s and %s disagree about the %s (%q and %q)",
					dirs[0], where, c.field, c.a, c.b)
			}
		}

		if first.SourceDateEpoch != s.SourceDateEpoch {
			return fmt.Errorf("stage: %s and %s disagree about the commit timestamp (%d and %d)",
				dirs[0], where, first.SourceDateEpoch, s.SourceDateEpoch)
		}
		if strings.Join(first.Matrix, ",") != strings.Join(s.Matrix, ",") {
			return fmt.Errorf("stage: %s and %s were planned for different matrices (%v and %v)",
				dirs[0], where, first.Matrix, s.Matrix)
		}

		// Both produce it from the same commit, so a difference means the
		// source they built was not the same source.
		if first.Source != nil && s.Source != nil && first.Source.SHA256 != s.Source.SHA256 {
			return fmt.Errorf(
				"stage: %s and %s produced different source archives, so they did not build the same tree",
				dirs[0], where)
		}
	}
	return nil
}

// coverage collects the targets the stages claim, refusing an overlap.
func coverage(dirs []string, stages []*Stage) (map[string]string, error) {
	covered := map[string]string{}

	for i, s := range stages {
		for _, target := range s.Targets {
			if where, seen := covered[target]; seen {
				return nil, fmt.Errorf("stage: %s was built by both %s and %s", target, where, dirs[i])
			}
			covered[target] = dirs[i]
		}
	}
	return covered, nil
}

// complete reports whether the stages between them cover the whole matrix.
func complete(matrix []string, covered map[string]string) error {
	var missing []string
	for _, target := range matrix {
		if _, ok := covered[target]; !ok {
			missing = append(missing, target)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	return fmt.Errorf(
		"stage: no staged directory built %s\n"+
			"  the release covers %s, and publishing without those would ship\n"+
			"  fewer platforms than the last release did",
		strings.Join(missing, ", "), strings.Join(matrix, ", "))
}
