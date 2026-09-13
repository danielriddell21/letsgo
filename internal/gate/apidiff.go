package gate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// ApidiffInstall is how to obtain the tool.
const ApidiffInstall = "go install golang.org/x/exp/cmd/apidiff@latest"

// ChangeKind distinguishes a change that can break a dependant from one that
// cannot.
type ChangeKind string

const (
	// Incompatible changes break code that compiled against the old version.
	Incompatible ChangeKind = "incompatible"

	// Compatible changes are additions.
	Compatible ChangeKind = "compatible"
)

// Change is one difference in a package's exported API.
type Change struct {
	Package string
	Kind    ChangeKind
	Text    string
}

func (c Change) String() string {
	if c.Package == "" {
		return c.Text
	}
	return c.Package + ": " + c.Text
}

// APIDiff compares the exported API of two checkouts of the same module.
//
// Go encodes compatibility in the import path: within a major version,
// removing or changing an exported symbol breaks every dependant at compile
// time, and the only sanctioned remedy is a new major version with a new path.
// Nothing in the toolchain enforces this, so the mistake is made quietly and
// discovered by other people.
//
// Both trees are compared package by package, because apidiff works on one
// package at a time and a module's surface is the union of its packages.
func APIDiff(ctx context.Context, oldDir, newDir string) ([]Change, error) {
	bin, err := find("apidiff", ApidiffInstall)
	if err != nil {
		return nil, err
	}

	packages, err := exportedPackages(ctx, newDir)
	if err != nil {
		return nil, err
	}

	var changes []Change
	for _, pkg := range packages {
		// A package that does not exist in the old tree is new, and an
		// addition cannot break anyone.
		if _, err := os.Stat(filepath.Join(oldDir, pkg.dir)); err != nil {
			continue
		}

		diff, err := comparePackage(ctx, bin, oldDir, newDir, pkg.dir)
		if err != nil {
			return nil, err
		}
		for i := range diff {
			diff[i].Package = pkg.importPath
		}
		changes = append(changes, diff...)
	}
	return changes, nil
}

// Incompatible reports the subset that breaks dependants.
func Incompatibles(changes []Change) []Change {
	var out []Change
	for _, c := range changes {
		if c.Kind == Incompatible {
			out = append(out, c)
		}
	}
	return out
}

type pkgRef struct {
	importPath string
	dir        string // relative to the module root
}

// exportedPackages lists the packages a dependant could import: everything
// except commands and anything under internal, neither of which anyone
// outside the module can depend on.
func exportedPackages(ctx context.Context, dir string) ([]pkgRef, error) {
	// Resolved to an absolute path and run with a fixed PATH, so neither this
	// command nor anything it execs can be chosen by a writable directory on
	// the caller's PATH.
	goBin, err := gobuild.Toolchain()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, goBin, "list", "-f", "{{.ImportPath}}\t{{.Name}}\t{{.Dir}}", "./...")
	cmd.Dir = dir
	cmd.Env = gobuild.Env(gobuild.Host(), "")

	out, err := output(cmd, false)
	if err != nil {
		return nil, err
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}

	var packages []pkgRef
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[1] == "main" {
			continue
		}
		importPath, pkgDir := fields[0], fields[2]

		if strings.HasPrefix(importPath, "internal/") ||
			strings.Contains(importPath, "/internal/") ||
			strings.HasSuffix(importPath, "/internal") {
			continue
		}

		rel, err := filepath.Rel(root, pkgDir)
		if err != nil {
			continue
		}
		packages = append(packages, pkgRef{importPath: importPath, dir: rel})
	}
	return packages, nil
}

// comparePackage compares one package between two checkouts.
//
// The two sides are separate modules, and a package can only be loaded from
// within the module that contains it — pointing one invocation at both
// directories fails, because whichever module the process happens to be in
// does not contain the other. So the old API is exported to a file from inside
// the old tree, and compared from inside the new one.
func comparePackage(ctx context.Context, bin, oldDir, newDir, relDir string) ([]Change, error) {
	exported, err := os.CreateTemp("", "letsgo-api-*")
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}
	_ = exported.Close()
	defer func() { _ = os.Remove(exported.Name()) }()

	pattern := "./" + filepath.ToSlash(relDir)

	write := exec.CommandContext(ctx, bin, "-w", exported.Name(), pattern)
	write.Dir = oldDir
	if _, err := output(write, false); err != nil {
		return nil, err
	}

	compare := exec.CommandContext(ctx, bin, exported.Name(), pattern)
	compare.Dir = newDir

	// apidiff exits non-zero when it finds incompatible changes.
	out, err := output(compare, true)
	if err != nil {
		return nil, err
	}
	return parseAPIDiff(string(out)), nil
}

// parseAPIDiff reads apidiff's report.
//
// The output is two optional sections, each a heading followed by indented
// lines. Anything outside them is noise.
func parseAPIDiff(out string) []Change {
	lines := strings.Split(out, "\n")
	changes := make([]Change, 0, len(lines))
	kind := ChangeKind("")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "Incompatible changes:"):
			kind = Incompatible
			continue
		case strings.HasPrefix(trimmed, "Compatible changes:"):
			kind = Compatible
			continue
		case kind == "":
			continue
		}

		// Entries are listed with a leading marker.
		text := strings.TrimPrefix(trimmed, "- ")
		changes = append(changes, Change{Kind: kind, Text: text})
	}
	return changes
}

// RequiredBump reports the smallest version change these API changes permit.
func RequiredBump(changes []Change) string {
	switch {
	case len(Incompatibles(changes)) > 0:
		return "major"
	case len(changes) > 0:
		return "minor"
	default:
		return "patch"
	}
}
