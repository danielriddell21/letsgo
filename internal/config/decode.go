package config

import (
	"fmt"
	"sort"
	"strings"
)

// Config is the semantic content of a letsgo.mod file.
//
// Every field is optional. A repository with no config file at all is the
// primary path, and each field here exists only to override something that
// could not be inferred (see internal/discover).
type Config struct {
	// Project overrides the project name derived from the module path.
	Project string

	// Targets overrides the default build matrix, as "goos/goarch" strings.
	// Parsing into gobuild.Target happens at plan time so that an invalid
	// target is reported alongside every other planning error rather than
	// aborting on the first one.
	Targets []string

	// LDFlags are appended to the defaults.
	LDFlags []string

	// ArchiveFiles are extra files to include in each archive.
	ArchiveFiles []string

	// Budgets caps binary size per target, e.g. "linux/amd64" -> "15MB".
	Budgets map[string]string

	// Image describes the container image to publish. Nil means none: a
	// release that creates a package in a registry should be something the
	// repository asked for.
	Image *Image

	// BrewTap is an "owner/repo" Homebrew tap to publish a formula to.
	BrewTap string

	// Prerelease is "auto", "true" or "false".
	Prerelease string

	// Draft creates the release without publishing it.
	Draft bool
}

// Image is the container image a release publishes.
type Image struct {
	// Reference overrides the default, which is derived from the repository.
	Reference string

	// Base is the image to stack on. Empty means scratch, which is the right
	// answer for a static binary that makes no TLS calls and the wrong one for
	// anything that does.
	Base string
}

// known lists every directive, with its arity described for error messages.
// A closed set is the point: an unrecognised directive is a mistake, and
// saying so immediately is better than ignoring it and producing a release
// that quietly does not match what the file asked for.
var known = map[string]string{
	"project": "project <name>",
	"build":   "build <goos/goarch>... or a build ( ... ) block",
	"ldflags": "ldflags <flag>...",
	"archive": "archive <file>... or an archive ( ... ) block",
	"budget":  "budget <goos/goarch> <size>",
	"image":   "image, image <reference>, or image base <reference>",
	"brew":    "brew <owner/tap-repo>",
	"release": "release <key=value>...",
}

// Decode interprets a parsed file.
func Decode(f *File) (*Config, error) {
	cfg := &Config{Budgets: map[string]string{}}
	seen := map[string]Position{}

	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *Comment:
			continue

		case *Block:
			if err := checkKnown(f.Name, s.Keyword, s.P); err != nil {
				return nil, err
			}
			if err := checkOnce(f.Name, seen, s.Keyword, s.P); err != nil {
				return nil, err
			}
			for _, line := range s.Lines {
				if err := apply(cfg, f.Name, line); err != nil {
					return nil, err
				}
			}

		case *Line:
			if err := checkKnown(f.Name, s.Keyword, s.P); err != nil {
				return nil, err
			}
			// Repeating a scalar directive is ambiguous: one of the two values
			// would silently win. Repeating a list directive is not.
			if isScalar(s.Keyword) {
				if err := checkOnce(f.Name, seen, s.Keyword, s.P); err != nil {
					return nil, err
				}
			}
			if err := apply(cfg, f.Name, s); err != nil {
				return nil, err
			}
		}
	}

	return cfg, nil
}

func isScalar(keyword string) bool {
	switch keyword {
	case "project", "brew":
		return true
	}
	return false
}

func checkKnown(file, keyword string, pos Position) error {
	if _, ok := known[keyword]; ok {
		return nil
	}

	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)

	msg := fmt.Sprintf("unknown directive %q; valid directives are %s",
		keyword, strings.Join(names, ", "))
	if near := nearestKeyword(keyword, names); near != "" {
		msg = fmt.Sprintf("unknown directive %q; did you mean %q?", keyword, near)
	}
	return errAt(file, pos, "%s", msg)
}

func nearestKeyword(keyword string, names []string) string {
	for _, name := range names {
		if strings.EqualFold(name, keyword) || strings.HasPrefix(name, keyword) {
			return name
		}
	}
	return ""
}

func checkOnce(file string, seen map[string]Position, keyword string, pos Position) error {
	if first, ok := seen[keyword]; ok {
		return errAt(file, pos, "%s is already set at line %d", keyword, first.Line)
	}
	seen[keyword] = pos
	return nil
}

func apply(cfg *Config, file string, line *Line) error {
	switch line.Keyword {
	case "project":
		if len(line.Args) != 1 {
			return arity(file, line)
		}
		cfg.Project = line.Args[0]

	case "build":
		if len(line.Args) == 0 {
			return arity(file, line)
		}
		cfg.Targets = append(cfg.Targets, line.Args...)

	case "ldflags":
		if len(line.Args) == 0 {
			return arity(file, line)
		}
		cfg.LDFlags = append(cfg.LDFlags, line.Args...)

	case "archive":
		if len(line.Args) == 0 {
			return arity(file, line)
		}
		cfg.ArchiveFiles = append(cfg.ArchiveFiles, line.Args...)

	case "budget":
		if len(line.Args) != 2 {
			return arity(file, line)
		}
		if _, exists := cfg.Budgets[line.Args[0]]; exists {
			return errAt(file, line.P, "budget for %s is already set", line.Args[0])
		}
		cfg.Budgets[line.Args[0]] = line.Args[1]

	case "image":
		if cfg.Image == nil {
			cfg.Image = &Image{}
		}
		switch {
		// A bare `image` asks for the default: a reference derived from the
		// repository, on scratch.
		case len(line.Args) == 0:

		case line.Args[0] == "base":
			if len(line.Args) != 2 {
				return arity(file, line)
			}
			if cfg.Image.Base != "" {
				return errAt(file, line.P, "image base is already set")
			}
			cfg.Image.Base = line.Args[1]

		case len(line.Args) == 1:
			if cfg.Image.Reference != "" {
				return errAt(file, line.P, "the image reference is already set")
			}
			cfg.Image.Reference = line.Args[0]

		default:
			return arity(file, line)
		}

	case "brew":
		if len(line.Args) != 1 {
			return arity(file, line)
		}
		if _, _, ok := strings.Cut(line.Args[0], "/"); !ok {
			return errAt(file, line.P, "brew tap %q must be in owner/repo form", line.Args[0])
		}
		cfg.BrewTap = line.Args[0]

	case "release":
		if len(line.Args) == 0 {
			return arity(file, line)
		}
		for _, arg := range line.Args {
			key, value, ok := strings.Cut(arg, "=")
			if !ok {
				return errAt(file, line.P, "release option %q must be key=value", arg)
			}
			switch key {
			case "prerelease":
				switch value {
				case "auto", "true", "false":
					cfg.Prerelease = value
				default:
					return errAt(file, line.P,
						"release prerelease must be auto, true or false, not %q", value)
				}
			case "draft":
				switch value {
				case "true":
					cfg.Draft = true
				case "false":
					cfg.Draft = false
				default:
					return errAt(file, line.P, "release draft must be true or false, not %q", value)
				}
			default:
				return errAt(file, line.P,
					"unknown release option %q; valid options are draft, prerelease", key)
			}
		}
	}
	return nil
}

func arity(file string, line *Line) error {
	return errAt(file, line.P, "%s takes %s", line.Keyword, known[line.Keyword])
}
