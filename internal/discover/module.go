// Package discover derives everything letsgo needs from what is already in the
// repository: go.mod, the git remote, the directory layout, and the source
// itself. A config file exists only to override what cannot be inferred.
package discover

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Module describes the Go module being released.
type Module struct {
	// Path is the module path, e.g. "github.com/danielriddell21/letsgo".
	Path string

	// Dir is the absolute path of the directory containing go.mod.
	Dir string

	// Name is the project name: the last element of the module path with any
	// major-version suffix removed.
	Name string

	// MajorSuffix is N from a trailing "/vN", or 0 when the path has none.
	MajorSuffix int
}

// FindModule walks up from start looking for go.mod.
func FindModule(start string) (Module, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Module{}, fmt.Errorf("discover: %w", err)
	}

	for {
		candidate := filepath.Join(dir, "go.mod")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			modPath, err := modulePath(candidate)
			if err != nil {
				return Module{}, err
			}
			name, major := splitMajorSuffix(modPath)
			return Module{Path: modPath, Dir: dir, Name: name, MajorSuffix: major}, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return Module{}, fmt.Errorf("discover: no go.mod found in %s or any parent directory", start)
		}
		dir = parent
	}
}

// modulePath reads the module directive from a go.mod file.
//
// This is a deliberately minimal reader rather than a full go.mod parser: we
// need exactly one directive, and taking a dependency to read one line would
// be a poor trade. The same line-and-block shape is what letsgo's own config
// format uses, so this is also a rehearsal for that parser.
func modulePath(goModPath string) (string, error) {
	f, err := os.Open(goModPath)
	if err != nil {
		return "", fmt.Errorf("discover: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inBlock := false

	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}

		if inBlock {
			if line == ")" {
				inBlock = false
				continue
			}
			return unquote(line), nil
		}

		rest, ok := strings.CutPrefix(line, "module")
		if !ok {
			continue
		}
		// Require a separator so that a directive such as "modules" is not
		// mistaken for "module".
		if rest != "" && !isSpace(rest[0]) {
			continue
		}

		rest = strings.TrimSpace(rest)
		if rest == "(" {
			inBlock = true
			continue
		}
		if rest != "" {
			return unquote(rest), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("discover: reading %s: %w", goModPath, err)
	}
	return "", fmt.Errorf("discover: %s has no module directive", goModPath)
}

func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if out, err := strconv.Unquote(s); err == nil {
			return out
		}
	}
	return s
}

// splitMajorSuffix separates a trailing "/vN" (N >= 2) from the project name.
func splitMajorSuffix(modulePath string) (name string, major int) {
	base := path.Base(modulePath)

	if n, ok := strings.CutPrefix(base, "v"); ok {
		if v, err := strconv.Atoi(n); err == nil && v >= 2 {
			parent := path.Base(path.Dir(modulePath))
			if parent != "." && parent != "/" {
				return parent, v
			}
		}
	}
	return base, 0
}

// CheckTag reports whether a tag's major version agrees with the module path.
//
// Go encodes major versions above v1 in the import path itself. Tagging v2.0.0
// on a module whose path has no /v2 suffix produces a release that `go get`
// will never resolve: the proxy serves it, pkg.go.dev lists it, and every
// attempt to depend on it silently selects the newest v1 instead. Nothing in
// the toolchain warns about this, and the failure surfaces as a user reporting
// that your latest release "doesn't exist".
//
// It is a two-line check, and it is one of the strongest arguments for a
// release tool that only has to understand one language.
func (m Module) CheckTag(tag string) error {
	major, err := majorFromTag(tag)
	if err != nil {
		return err
	}

	switch {
	case major >= 2 && m.MajorSuffix == 0:
		return fmt.Errorf(
			"tag %s is a v%d release, but module path %q has no /v%d suffix\n"+
				"  `go get %s@%s` would silently resolve to the latest v1 instead\n"+
				"  fix: change the module directive to %s/v%d and retag",
			tag, major, m.Path, major, m.Path, tag, m.Path, major)

	case major >= 2 && m.MajorSuffix != major:
		return fmt.Errorf(
			"tag %s is a v%d release, but module path %q declares /v%d",
			tag, major, m.Path, m.MajorSuffix)

	case major < 2 && m.MajorSuffix != 0:
		return fmt.Errorf(
			"module path %q declares /v%d, but tag %s is a v%d release",
			m.Path, m.MajorSuffix, tag, major)
	}
	return nil
}

func majorFromTag(tag string) (int, error) {
	v, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return 0, fmt.Errorf("tag %q does not start with %q", tag, "v")
	}
	digits, _, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("tag %q has no numeric major version", tag)
	}
	return major, nil
}
