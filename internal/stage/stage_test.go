package stage_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/stage"
)

// staged writes a stage description and returns its directory.
func staged(t *testing.T, s *stage.Stage) string {
	t.Helper()
	dir := t.TempDir()
	if s.Schema == 0 {
		s.Schema = manifest.Schema
	}
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func share(targets []string, artifacts ...string) *stage.Stage {
	s := &stage.Stage{
		Project: "split", Version: "1.0.0", Tag: "v1.0.0", Commit: "abc123",
		SourceDateEpoch: 1710505845,
		Matrix:          []string{"linux/amd64", "linux/arm64", "windows/amd64"},
		Targets:         targets,
		Builder:         manifest.Builder{Tool: "letsgo test", Go: "go1.27.1"},
		Source:          &manifest.Source{Archive: "split_1.0.0_source.tar.gz", SHA256: "src"},
	}
	for i, name := range artifacts {
		goos, arch, _ := strings.Cut(targets[min(i, len(targets)-1)], "/")
		s.Artifacts = append(s.Artifacts, manifest.Artifact{
			Name: name, OS: goos, Arch: arch, Binary: "split", SHA256: name,
		})
	}
	return s
}

func TestMergeCombinesTheShares(t *testing.T) {
	a := staged(t, share([]string{"linux/amd64", "linux/arm64"}, "a.tar.gz", "b.tar.gz"))
	b := staged(t, share([]string{"windows/amd64"}, "c.zip"))

	merged, err := stage.Merge([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}

	if len(merged.Manifest.Artifacts) != 3 {
		t.Fatalf("artifacts = %+v", merged.Manifest.Artifacts)
	}
	// Sorted, so the result does not depend on which machine finished first.
	names := make([]string, len(merged.Manifest.Artifacts))
	for i, a := range merged.Manifest.Artifacts {
		names[i] = a.Name
	}
	if strings.Join(names, ",") != "a.tar.gz,b.tar.gz,c.zip" {
		t.Errorf("artifacts = %q", names)
	}
	if merged.From["c.zip"] != b {
		t.Errorf("c.zip came from %q, want %q", merged.From["c.zip"], b)
	}
	if merged.Manifest.Source == nil || merged.Manifest.Source.SHA256 != "src" {
		t.Errorf("source = %+v", merged.Manifest.Source)
	}
}

// The check the whole design exists for: a release assembled from pieces must
// not quietly ship fewer platforms than it promised.
func TestMergeRefusesAnIncompleteMatrix(t *testing.T) {
	only := staged(t, share([]string{"linux/amd64", "linux/arm64"}, "a.tar.gz", "b.tar.gz"))

	_, err := stage.Merge([]string{only})
	if err == nil {
		t.Fatal("merging a partial release should have been refused")
	}
	if !strings.Contains(err.Error(), "windows/amd64") {
		t.Errorf("error = %q, want it to name the missing target", err)
	}
}

// Stages that disagree built different source, which is the one mistake that
// would otherwise produce a release whose parts do not belong together.
func TestMergeRefusesStagesThatDisagree(t *testing.T) {
	base := func() *stage.Stage { return share([]string{"linux/amd64"}, "a.tar.gz") }

	tests := []struct {
		name   string
		change func(*stage.Stage)
		want   string
	}{
		{"a different commit", func(s *stage.Stage) { s.Commit = "def456" }, "commit"},
		{"a different version", func(s *stage.Stage) { s.Version = "2.0.0" }, "version"},
		{"a different toolchain", func(s *stage.Stage) { s.Builder.Go = "go1.26.8" }, "go toolchain"},
		{"a different timestamp", func(s *stage.Stage) { s.SourceDateEpoch = 1 }, "commit timestamp"},
		{"a different matrix", func(s *stage.Stage) { s.Matrix = []string{"linux/amd64"} }, "matrices"},
		{
			"different source archives",
			func(s *stage.Stage) { s.Source = &manifest.Source{Archive: "x", SHA256: "other"} },
			"different source archives",
		},
	}

	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			other := share([]string{"linux/arm64", "windows/amd64"}, "b.tar.gz", "c.zip")
			c.change(other)

			_, err := stage.Merge([]string{staged(t, base()), staged(t, other)})
			if err == nil {
				t.Fatal("stages that disagree should not have merged")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// Two machines building the same target means the config told them to, and
// whichever won would be arbitrary.
func TestMergeRefusesAnOverlap(t *testing.T) {
	a := staged(t, share([]string{"linux/amd64", "linux/arm64", "windows/amd64"}, "a.tar.gz"))
	b := staged(t, share([]string{"linux/amd64"}, "b.tar.gz"))

	if _, err := stage.Merge([]string{a, b}); err == nil ||
		!strings.Contains(err.Error(), "built by both") {
		t.Errorf("err = %v", err)
	}
}

func TestReadRejectsAnUnknownSchema(t *testing.T) {
	dir := staged(t, &stage.Stage{Schema: 99})
	if _, err := stage.Read(dir); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Errorf("err = %v", err)
	}
	if _, err := stage.Read(filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing stage should be an error")
	}
}
