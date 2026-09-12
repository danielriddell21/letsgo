// Package diff compares two releases.
//
// It answers what actually changed in terms of the artifacts — how much larger
// the binary got, which dependency arrived, what the exported API gained or
// lost, whether the toolchain moved — rather than in terms of what the commit
// messages claimed.
//
// Nothing is rebuilt and nothing is downloaded. Both manifests already record
// everything compared here, which is most of the reason they record it.
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/manifest"
)

// Result is the difference between two releases.
type Result struct {
	From, To string

	Sizes        []SizeChange
	Dependencies []DepChange
	API          []manifest.APIChange
	Toolchain    *ToolchainChange

	// SizeKind names what Sizes measures, because the answer differs: it is
	// "binary size" wherever both releases recorded it, and "archive size"
	// against a release published before letsgo did.
	SizeKind string
}

// SizeChange is one artifact's change in size.
type SizeChange struct {
	Target string
	From   int64
	To     int64
}

// Delta is the change in bytes, negative when the artifact shrank.
func (s SizeChange) Delta() int64 { return s.To - s.From }

// Percent is the change as a proportion of the original.
func (s SizeChange) Percent() float64 {
	if s.From == 0 {
		return 0
	}
	return float64(s.Delta()) / float64(s.From) * 100
}

// DepKind says how a dependency changed.
type DepKind string

const (
	DepAdded   DepKind = "added"
	DepRemoved DepKind = "removed"
	DepChanged DepKind = "changed"
)

// DepChange is one dependency's arrival, departure or version change.
type DepChange struct {
	Kind     DepKind
	Path     string
	From, To string
}

// ToolchainChange records a compiler change, which explains size differences
// that no dependency accounts for.
type ToolchainChange struct{ From, To string }

// Compare produces the difference between two releases.
func Compare(from, to *manifest.Manifest) *Result {
	r := &Result{From: from.Version, To: to.Version, API: to.APIChanges}

	compareSizes(r, from, to)
	compareDependencies(r, from, to)

	if from.Builder.Go != to.Builder.Go {
		r.Toolchain = &ToolchainChange{From: from.Builder.Go, To: to.Builder.Go}
	}
	return r
}

// Empty reports whether the releases are indistinguishable in every dimension
// compared here.
func (r *Result) Empty() bool {
	return len(r.Sizes) == 0 && len(r.Dependencies) == 0 &&
		len(r.API) == 0 && r.Toolchain == nil
}

func compareSizes(r *Result, from, to *manifest.Manifest) {
	// Archive size moves with the compressor as well as with the code, so the
	// binary is the honest number wherever both sides recorded one. Releases
	// published before letsgo recorded it leave only the archive.
	binary := recordsBinarySize(from) && recordsBinarySize(to)
	r.SizeKind = "archive size"
	if binary {
		r.SizeKind = "binary size"
	}
	sizeOf := func(a manifest.Artifact) int64 {
		if binary {
			return a.BinarySize
		}
		return a.Size
	}

	previous := make(map[string]int64, len(from.Artifacts))
	for _, a := range from.Artifacts {
		previous[a.OS+"/"+a.Arch] = sizeOf(a)
	}

	for _, a := range to.Artifacts {
		target := a.OS + "/" + a.Arch
		before, existed := previous[target]
		// A target that did not exist before has no size to compare against;
		// reporting it as infinite growth would be noise.
		if !existed || before == sizeOf(a) {
			continue
		}
		r.Sizes = append(r.Sizes, SizeChange{Target: target, From: before, To: sizeOf(a)})
	}
	sort.Slice(r.Sizes, func(i, j int) bool { return r.Sizes[i].Target < r.Sizes[j].Target })
}

// recordsBinarySize reports whether every artifact carries a binary size.
// Partial coverage is treated as none: a table mixing the two measurements
// would compare artifacts against each other that were never comparable.
func recordsBinarySize(m *manifest.Manifest) bool {
	if len(m.Artifacts) == 0 {
		return false
	}
	for _, a := range m.Artifacts {
		if a.BinarySize == 0 {
			return false
		}
	}
	return true
}

func compareDependencies(r *Result, from, to *manifest.Manifest) {
	before := map[string]string{}
	for _, m := range from.Modules.List {
		before[m.Path] = m.Version
	}
	after := map[string]string{}
	for _, m := range to.Modules.List {
		after[m.Path] = m.Version
	}

	for path, version := range after {
		switch previous, existed := before[path]; {
		case !existed:
			r.Dependencies = append(r.Dependencies, DepChange{Kind: DepAdded, Path: path, To: version})
		case previous != version:
			r.Dependencies = append(r.Dependencies,
				DepChange{Kind: DepChanged, Path: path, From: previous, To: version})
		}
	}
	for path, version := range before {
		if _, still := after[path]; !still {
			r.Dependencies = append(r.Dependencies, DepChange{Kind: DepRemoved, Path: path, From: version})
		}
	}

	sort.Slice(r.Dependencies, func(i, j int) bool {
		return r.Dependencies[i].Path < r.Dependencies[j].Path
	})
}

// String renders the comparison.
func (r *Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s -> %s\n\n", r.From, r.To)

	if r.Empty() {
		b.WriteString("no difference in size, dependencies, API or toolchain\n")
		return b.String()
	}

	if len(r.Sizes) > 0 {
		b.WriteString(r.SizeKind + "\n")
		width := 0
		for _, s := range r.Sizes {
			width = max(width, len(s.Target))
		}
		for _, s := range r.Sizes {
			fmt.Fprintf(&b, "  %-*s  %8s -> %-8s  %+.0f%%\n",
				width, s.Target, bytesize.Size(s.From), bytesize.Size(s.To), s.Percent())
		}
		b.WriteString("\n")
	}

	if len(r.Dependencies) > 0 {
		b.WriteString("dependencies\n")
		for _, d := range r.Dependencies {
			switch d.Kind {
			case DepAdded:
				fmt.Fprintf(&b, "  + %s %s\n", d.Path, d.To)
			case DepRemoved:
				fmt.Fprintf(&b, "  - %s %s\n", d.Path, d.From)
			default:
				fmt.Fprintf(&b, "  ~ %s %s -> %s\n", d.Path, d.From, d.To)
			}
		}
		b.WriteString("\n")
	}

	if len(r.API) > 0 {
		b.WriteString("api\n")
		for _, c := range r.API {
			marker := "+"
			if c.Kind == "incompatible" {
				marker = "!"
			}
			if c.Package != "" {
				fmt.Fprintf(&b, "  %s %s: %s\n", marker, c.Package, c.Text)
				continue
			}
			fmt.Fprintf(&b, "  %s %s\n", marker, c.Text)
		}
		b.WriteString("\n")
	}

	if r.Toolchain != nil {
		// Listed last but often the explanation: a compiler change moves every
		// binary at once, which no dependency accounts for.
		fmt.Fprintf(&b, "toolchain\n  %s -> %s\n\n", r.Toolchain.From, r.Toolchain.To)
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}
