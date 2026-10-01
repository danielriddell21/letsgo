package diff

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/manifest"
)

func diffFixture() *Result {
	from := manifestWith("v1.2.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 8_100_000, BinarySize: 8_100_000},
	}, []manifest.Module{
		{Path: "example.com/gone", Version: "v1.0.0"},
		{Path: "example.com/moved", Version: "v0.25.0"},
	}, "go1.26.1")
	to := manifestWith("v1.3.0", []manifest.Artifact{
		{OS: "linux", Arch: "amd64", Size: 8_400_000, BinarySize: 8_400_000},
	}, []manifest.Module{
		{Path: "example.com/arrived", Version: "v0.9.0"},
		{Path: "example.com/moved", Version: "v0.26.0"},
	}, "go1.26.2")
	to.APIChanges = []manifest.APIChange{{Kind: "compatible", Package: "example.com/p", Text: "New: added"}}
	return Compare(from, to)
}

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

func TestMarkdownRendersOneCollapsedTable(t *testing.T) {
	out := diffFixture().Markdown()

	want := "| | |\n" +
		"|---|---|\n" +
		"| toolchain | go1.26.1 → go1.26.2 |\n" +
		"| deps | + example.com/arrived v0.9.0 · − example.com/gone v1.0.0 · ↑ example.com/moved v0.25.0 → v0.26.0 |\n" +
		"| size | linux/amd64 7.7 MB → 8.0 MB (+3.7%) |\n" +
		"| api | + example.com/p: New: added |\n"
	if out != want {
		t.Errorf("markdown =\n%s\nwant\n%s", out, want)
	}
}

func TestDepArrow(t *testing.T) {
	for _, tt := range []struct{ from, to, want string }{
		{"v1.1.0", "v1.0.0", "↓"},
		{"v1.0.0", "v1.1.0", "↑"},
		{"v1.0.0", "not-a-version", "~"},
	} {
		if got := depArrow(tt.from, tt.to); got != tt.want {
			t.Errorf("depArrow(%q, %q) = %q, want %q", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestMarkdownAPIMarksIncompatibleChanges(t *testing.T) {
	out := markdownAPI([]manifest.APIChange{{Kind: "incompatible", Text: "Old: removed"}})
	if want := "! Old: removed"; out != want {
		t.Errorf("markdownAPI = %q, want %q", out, want)
	}
}

func TestMarkdownIsEmptyWhenIdentical(t *testing.T) {
	from := manifestWith("v1.0.0", nil, nil, "go1.26.8")
	to := manifestWith("v1.0.1", nil, nil, "go1.26.8")

	if out := Compare(from, to).Markdown(); out != "" {
		t.Errorf("markdown = %q, want empty", out)
	}
}

func TestNotesWrapsTheTableInACollapsedDetailsBlock(t *testing.T) {
	out := diffFixture().Notes("v1.2.0")

	want := "<details><summary>What shipped (vs v1.2.0)</summary>\n\n" +
		"| | |\n" +
		"|---|---|\n" +
		"| toolchain | go1.26.1 → go1.26.2 |\n" +
		"| deps | + example.com/arrived v0.9.0 · − example.com/gone v1.0.0 · ↑ example.com/moved v0.25.0 → v0.26.0 |\n" +
		"| size | linux/amd64 7.7 MB → 8.0 MB (+3.7%) |\n" +
		"</details>\n"
	if out != want {
		t.Errorf("notes =\n%s\nwant\n%s", out, want)
	}
	if strings.Contains(out, "example.com/p") {
		t.Error("notes must not show API changes; the changelog already does")
	}
}

func TestNotesIsEmptyWhenNothingSurvivesTheFilter(t *testing.T) {
	from := manifestWith("v1.0.0", nil, nil, "go1.26.8")
	to := manifestWith("v1.0.1", nil, nil, "go1.26.8")

	if out := Compare(from, to).Notes("v1.0.0"); out != "" {
		t.Errorf("notes = %q, want empty", out)
	}
}

func TestNotableSizesFiltersCapsAndSortsLargestFirst(t *testing.T) {
	sizes := []SizeChange{
		{Target: "a", From: 1000, To: 1005}, // +0.5%, below the threshold
		{Target: "b", From: 1000, To: 1300}, // +30%
		{Target: "c", From: 1000, To: 1120}, // +12%
		{Target: "d", From: 1000, To: 1200}, // +20%
		{Target: "e", From: 1000, To: 1150}, // +15%
		{Target: "f", From: 1000, To: 1050}, // +5%, bumped by the cap
		{Target: "g", From: 1000, To: 920},  // -8%
	}

	got := notableSizes(sizes)

	var order []string
	for _, s := range got {
		order = append(order, s.Target)
	}
	want := []string{"b", "d", "e", "c", "g"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, target := range want {
		if order[i] != target {
			t.Errorf("order = %v, want %v", order, want)
			break
		}
	}
}

func TestJSONCarriesSchemaAndEveryField(t *testing.T) {
	data, err := diffFixture().JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	out := string(data)
	for _, want := range []string{
		`"schema": 1`,
		`"from": "v1.2.0"`,
		`"to": "v1.3.0"`,
		`"toolchain"`, `"go1.26.1"`, `"go1.26.2"`,
		`"dependencies"`, `"kind": "added"`, `"example.com/arrived"`,
		`"sizes"`, `"target": "linux/amd64"`,
		`"size_kind": "binary size"`,
		`"api"`, `"example.com/p"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("json is missing %q:\n%s", want, out)
		}
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
