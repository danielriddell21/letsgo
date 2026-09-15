package repro_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/repro"
	"github.com/danielriddell21/letsgo/internal/zig"
)

// A fixed instant standing in for a commit timestamp. Nothing in a release may
// depend on the wall clock, so this value is the only time the pipeline sees.
var commitTime = time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC)

func options(t *testing.T, sourceDir, workDir string) repro.Options {
	t.Helper()
	return repro.Options{
		ModuleDir:  sourceDir,
		Commands:   []repro.Command{{Package: ".", Binary: "fixture"}},
		Name:       "fixture",
		Version:    "1.2.3",
		Commit:     "9f2ab1c",
		ModTime:    commitTime,
		ExtraFiles: []string{"README.md", "LICENSE"},
		WorkDir:    workDir,
	}
}

// build runs the pipeline in an environment deliberately unlike the previous
// run's: a different source directory, a different work directory, and a
// different temporary directory.
func build(t *testing.T, label string, coldCache bool) []repro.Artifact {
	t.Helper()

	root := t.TempDir()
	source := filepath.Join(root, "src-"+label)
	work := filepath.Join(root, "work-"+label)
	tmp := filepath.Join(root, "tmp-"+label)

	copyDir(t, "testdata/fixture", source)
	mkdir(t, tmp)

	// -trimpath is what makes the source path invisible to the compiler. Using
	// a different path each run is how we find out whether it is working.
	t.Setenv("TMPDIR", tmp)
	if coldCache {
		cache := filepath.Join(root, "gocache-"+label)
		mkdir(t, cache)
		t.Setenv("GOCACHE", cache)
	}

	artifacts, err := repro.Build(context.Background(), options(t, source, work))
	if err != nil {
		t.Fatalf("build %s: %v", label, err)
	}
	if len(artifacts) == 0 {
		t.Fatalf("build %s produced no artifacts", label)
	}
	return artifacts
}

// This is the property the entire tool rests on. If it does not hold, letsgo
// cannot verify a release, cannot safely resume an upload, and has no reason
// to exist in preference to GoReleaser.
func TestReproducibleAcrossRuns(t *testing.T) {
	first := build(t, "a", false)

	// Cross a whole-second boundary so that any reliance on the clock — in the
	// compiler, the archive writer, or our own code — has a chance to show up.
	time.Sleep(1100 * time.Millisecond)

	second := build(t, "b", false)
	compare(t, "fixture", first, second)
}

// The build cache is a plausible hiding place for nondeterminism: a cached
// object produced under one set of conditions could differ from a freshly
// compiled one. Building from an empty cache proves the output does not depend
// on what the machine happened to have compiled before.
func TestReproducibleFromColdCache(t *testing.T) {
	if testing.Short() {
		t.Skip("recompiles the standard library twice")
	}
	first := build(t, "cold-a", true)
	second := build(t, "cold-b", true)
	compare(t, "fixture", first, second)
}

// Injected version metadata has to actually reach the binary. Checking the
// symbol exists is a structural test; running the thing is an empirical one,
// and this is the seed of the smoke-test gate described in the wiki's Design
// page.
func TestVersionMetadataReachesTheBinary(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	work := filepath.Join(root, "work")
	copyDir(t, "testdata/fixture", source)

	opts := options(t, source, work)
	opts.Targets = []gobuild.Target{gobuild.Host()}

	if _, err := repro.Build(context.Background(), opts); err != nil {
		t.Fatalf("build: %v", err)
	}

	host := gobuild.Host()
	bin := filepath.Join(work, "fixture_"+host.OS+"_"+host.Arch, "fixture"+host.Ext())

	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("running %s: %v", bin, err)
	}

	got := strings.TrimSpace(string(out))
	want := "fixture 1.2.3 (9f2ab1c) built 2024-03-15T12:30:45Z"
	if got != want {
		t.Errorf("binary reported %q, want %q", got, want)
	}
}

// compare asserts that two runs of the same release agree on everything they
// publish: the archives, the image assembled from them, and the SBOM.
func compare(t *testing.T, binary string, first, second []repro.Artifact) {
	t.Helper()

	if len(first) != len(second) {
		t.Fatalf("artifact count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		compareArtifact(t, i, first[i], second[i])
	}
	compareImage(t, binary, first, second)
	compareSBOM(t, first, second)

	if !t.Failed() {
		t.Logf("%d artifacts, the image and the SBOM reproduced byte for byte on %s/%s",
			len(first), runtime.GOOS, runtime.GOARCH)
	}
}

// compareArtifact reports the binaries before the archive. If they match and
// the archive does not, the archive writer is at fault; if they differ,
// nothing downstream of the compiler is worth investigating yet.
func compareArtifact(t *testing.T, i int, a, b repro.Artifact) {
	t.Helper()

	if a.Archive != b.Archive {
		t.Errorf("artifact %d name differs: %s vs %s", i, a.Archive, b.Archive)
		return
	}
	if len(a.Binaries) != len(b.Binaries) {
		t.Errorf("%s: %d binaries vs %d", a.Target, len(a.Binaries), len(b.Binaries))
		return
	}

	differed := false
	for j := range a.Binaries {
		if a.Binaries[j].SHA256 != b.Binaries[j].SHA256 {
			t.Errorf("%s: %s is not reproducible\n  run 1: %s\n  run 2: %s",
				a.Target, a.Binaries[j].Name, a.Binaries[j].SHA256, b.Binaries[j].SHA256)
			differed = true
		}
	}
	if differed {
		return
	}
	if a.ArchiveSHA256 != b.ArchiveSHA256 {
		t.Errorf("%s: binary matches but archive does not — the archive writer is leaking state\n  run 1: %s\n  run 2: %s",
			a.Target, a.ArchiveSHA256, b.ArchiveSHA256)
	}
}

// compareImage checks the container image, which is published with the same
// promise and assembled from these same binaries — so if it does not
// reproduce, the fault is in the layer writer or the config, and that
// distinction is worth having.
func compareImage(t *testing.T, binary string, first, second []repro.Artifact) {
	t.Helper()

	firstImage, err := repro.ImageDigest(first, binary, commitTime)
	if err != nil {
		t.Fatalf("assembling the first image: %v", err)
	}
	secondImage, err := repro.ImageDigest(second, binary, commitTime)
	if err != nil {
		t.Fatalf("assembling the second image: %v", err)
	}
	if firstImage != secondImage {
		t.Errorf("the container image is not reproducible\n  run 1: %s\n  run 2: %s",
			firstImage, secondImage)
	}
}

// compareSBOM checks the dependency document, usually the least reproducible
// file in a release: the conventional generators stamp a wall clock and a
// random serial into every run.
func compareSBOM(t *testing.T, first, second []repro.Artifact) {
	t.Helper()

	firstSBOM, err := repro.SBOMDigest(options(t, "", ""), "go1.27.1", first)
	if err != nil {
		t.Fatalf("generating the first SBOM: %v", err)
	}
	secondSBOM, err := repro.SBOMDigest(options(t, "", ""), "go1.27.1", second)
	if err != nil {
		t.Fatalf("generating the second SBOM: %v", err)
	}
	if firstSBOM != secondSBOM {
		t.Errorf("the SBOM is not reproducible\n  run 1: %s\n  run 2: %s", firstSBOM, secondSBOM)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	mkdir(t, dst)

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, from, to)
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// §7 tier 1 for cgo: one machine must agree with itself across runs that vary
// the work directory and the build cache.
//
// Tier 2 — three machines agreeing with each other — is the cross-machine job
// in CI, which records these same digests from reprodigest on Linux, macOS and
// Windows and diffs them. That is the claim that matters, because it is the one
// a third party relies on; this is the cheaper check that fails first when the
// C toolchain stops being pinned properly.
//
// Gated because it downloads a 50MB toolchain, which is not something every
// `go test ./...` should do.
func TestCgoIsReproducibleAcrossRuns(t *testing.T) {
	if os.Getenv("LETSGO_ZIG_DOWNLOAD") == "" {
		t.Skip("set LETSGO_ZIG_DOWNLOAD=1 to exercise the cgo build")
	}

	toolchain, err := zig.Ensure(t.Context(), "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	options := func() repro.Options {
		return repro.Options{
			ModuleDir:  "testdata/cgofixture",
			Commands:   []repro.Command{{Package: ".", Binary: "cgofixture"}},
			Name:       "cgofixture",
			Version:    "1.2.3",
			Commit:     "9f2ab1c",
			ModTime:    time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC),
			Targets:    []gobuild.Target{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}},
			ExtraFiles: []string{"README.md", "LICENSE"},
			CGo:        toolchain,
			WorkDir:    t.TempDir(),
		}
	}

	first, err := repro.Build(t.Context(), options())
	if err != nil {
		t.Fatal(err)
	}
	second, err := repro.Build(t.Context(), options())
	if err != nil {
		t.Fatal(err)
	}

	if len(first) == 0 {
		t.Fatal("no artifacts were built")
	}
	compare(t, "cgofixture", first, second)
}
