package diff

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
)

func manifestWith(version string, artifacts []manifest.Artifact, modules []manifest.Module, goVersion string) *manifest.Manifest {
	return &manifest.Manifest{
		Schema:    manifest.Schema,
		Version:   version,
		Builder:   manifest.Builder{Tool: "letsgo", Go: goVersion},
		Modules:   manifest.Modules{Count: len(modules), List: modules},
		Artifacts: artifacts,
	}
}

func TestCompareReportsSizeChanges(t *testing.T) {
	from := manifestWith("v1.0.0", []manifest.Artifact{
		{Name: "a_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Size: 1000},
		{Name: "a_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Size: 2000},
	}, nil, "go1.26.8")
	to := manifestWith("v1.1.0", []manifest.Artifact{
		{Name: "a_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Size: 1200},
		{Name: "a_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Size: 2000},
	}, nil, "go1.26.8")

	r := Compare(from, to)

	if len(r.Sizes) != 1 {
		t.Fatalf("want 1 size change, got %d: %+v", len(r.Sizes), r.Sizes)
	}
	change := r.Sizes[0]
	if change.Target != "linux/amd64" {
		t.Errorf("target = %q, want linux/amd64", change.Target)
	}
	if change.Delta() != 200 {
		t.Errorf("delta = %d, want 200", change.Delta())
	}
	if change.Percent() != 20 {
		t.Errorf("percent = %v, want 20", change.Percent())
	}
}

// A target that is new in the later release has no previous size, and
// reporting one would mean inventing a baseline.
func TestCompareIgnoresNewTargets(t *testing.T) {
	from := manifestWith("v1.0.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000},
	}, nil, "go1.26.8")
	to := manifestWith("v1.1.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000},
		{OS: "linux", Arch: "arm64", Size: 900},
	}, nil, "go1.26.8")

	if r := Compare(from, to); len(r.Sizes) != 0 {
		t.Fatalf("want no size changes, got %+v", r.Sizes)
	}
}

func TestCompareReportsDependencyChanges(t *testing.T) {
	from := manifestWith("v1.0.0", nil, []manifest.Module{
		{Path: "example.com/gone", Version: "v1.0.0"},
		{Path: "example.com/moved", Version: "v1.0.0"},
		{Path: "example.com/same", Version: "v1.0.0"},
	}, "go1.26.8")
	to := manifestWith("v1.1.0", nil, []manifest.Module{
		{Path: "example.com/arrived", Version: "v0.2.0"},
		{Path: "example.com/moved", Version: "v1.1.0"},
		{Path: "example.com/same", Version: "v1.0.0"},
	}, "go1.26.8")

	r := Compare(from, to)

	want := []DepChange{
		{Kind: DepAdded, Path: "example.com/arrived", To: "v0.2.0"},
		{Kind: DepRemoved, Path: "example.com/gone", From: "v1.0.0"},
		{Kind: DepChanged, Path: "example.com/moved", From: "v1.0.0", To: "v1.1.0"},
	}
	if len(r.Dependencies) != len(want) {
		t.Fatalf("want %d changes, got %+v", len(want), r.Dependencies)
	}
	for i, w := range want {
		if r.Dependencies[i] != w {
			t.Errorf("change %d = %+v, want %+v", i, r.Dependencies[i], w)
		}
	}
}

// Binary size is the honest measurement, so it wins wherever both releases
// recorded one.
func TestCompareUsesBinarySizeWhenBothRecordIt(t *testing.T) {
	from := manifestWith("v1.0.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000, BinarySize: 2000},
	}, nil, "go1.26.8")
	to := manifestWith("v1.1.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000, BinarySize: 2500},
	}, nil, "go1.26.8")

	r := Compare(from, to)

	if r.SizeKind != "binary size" {
		t.Errorf("size kind = %q, want binary size", r.SizeKind)
	}
	if len(r.Sizes) != 1 || r.Sizes[0].Delta() != 500 {
		t.Fatalf("sizes = %+v", r.Sizes)
	}
}

// Releases published before letsgo recorded binary size leave only the
// archive, and half a table of each would compare numbers that never meant
// the same thing.
func TestCompareFallsBackToArchiveSize(t *testing.T) {
	from := manifestWith("v1.0.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000},
	}, nil, "go1.26.8")
	to := manifestWith("v1.1.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1200, BinarySize: 2500},
	}, nil, "go1.26.8")

	r := Compare(from, to)

	if r.SizeKind != "archive size" {
		t.Errorf("size kind = %q, want archive size", r.SizeKind)
	}
	if len(r.Sizes) != 1 || r.Sizes[0].Delta() != 200 {
		t.Fatalf("sizes = %+v", r.Sizes)
	}
}

func TestCompareReportsToolchainChange(t *testing.T) {
	from := manifestWith("v1.0.0", nil, nil, "go1.26.7")
	to := manifestWith("v1.1.0", nil, nil, "go1.26.8")

	r := Compare(from, to)

	if r.Toolchain == nil {
		t.Fatal("want a toolchain change")
	}
	if r.Toolchain.From != "go1.26.7" || r.Toolchain.To != "go1.26.8" {
		t.Errorf("toolchain = %+v", *r.Toolchain)
	}
}

func TestCompareCarriesAPIChanges(t *testing.T) {
	from := manifestWith("v1.0.0", nil, nil, "go1.26.8")
	to := manifestWith("v2.0.0", nil, nil, "go1.26.8")
	to.APIChanges = []manifest.APIChange{
		{Kind: "incompatible", Package: "example.com/p", Text: "Old: removed"},
	}

	r := Compare(from, to)

	if len(r.API) != 1 || r.API[0].Kind != "incompatible" {
		t.Fatalf("api = %+v", r.API)
	}
	if r.Empty() {
		t.Error("a release with API changes is not empty")
	}
}

func TestEmptyWhenIdentical(t *testing.T) {
	from := manifestWith("v1.0.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000},
	}, []manifest.Module{{Path: "example.com/a", Version: "v1.0.0"}}, "go1.26.8")
	to := manifestWith("v1.0.1", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 1000},
	}, []manifest.Module{{Path: "example.com/a", Version: "v1.0.0"}}, "go1.26.8")

	r := Compare(from, to)

	if !r.Empty() {
		t.Fatalf("want empty, got %+v", r)
	}
	if !strings.Contains(r.String(), "no difference") {
		t.Errorf("rendering = %q", r.String())
	}
}

func TestStringRendersEverySection(t *testing.T) {
	from := manifestWith("v1.0.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 2 << 20, BinarySize: 4 << 20},
	}, []manifest.Module{{Path: "example.com/gone", Version: "v1.0.0"}}, "go1.26.7")
	to := manifestWith("v1.1.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 2 << 20, BinarySize: 5 << 20},
	}, []manifest.Module{{Path: "example.com/new", Version: "v1.0.0"}}, "go1.26.8")
	to.APIChanges = []manifest.APIChange{{Kind: "compatible", Package: "example.com/p", Text: "New: added"}}

	out := Compare(from, to).String()

	for _, want := range []string{
		"v1.0.0 -> v1.1.0",
		"binary size", "linux/amd64", "+25%",
		"dependencies", "+ example.com/new", "- example.com/gone",
		"api", "+ example.com/p: New: added",
		"toolchain", "go1.26.7 -> go1.26.8",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendering is missing %q:\n%s", want, out)
		}
	}
}
