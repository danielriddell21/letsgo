package discover

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/semver"
)

// Scope identifies which part of a repository a release covers.
//
// A module at the repository's own root has an empty Scope: nothing prefixes
// its tags, exactly as every single-module repository works today. A module
// nested in a monorepo — one Go already versions independently, by tags under
// its own directory — carries that directory as Dir, and Dir with a trailing
// slash as Prefix: the part of a version tag before the "vX.Y.Z" Go itself
// expects.
type Scope struct {
	// Dir is the module's directory, slash-separated and relative to the
	// repository's git top level. Empty for a root module.
	Dir string

	// Prefix is Dir with a trailing slash, or empty for a root module. A
	// version tag naming this module is Prefix + "vX.Y.Z".
	Prefix string
}

// NewScope derives a module's scope from its own directory and the git top
// level of the repository containing it, both absolute.
//
// Each is resolved through its symlinks before being compared: git's own
// --show-toplevel already canonicalizes its answer (through macOS's
// /tmp -> /private/tmp, a Windows short name, or any other alias), and a
// caller's own directory usually has not. Comparing the two textually
// without matching that would call every module "outside" its own
// repository, on every platform where the two happen to disagree.
func NewScope(topLevel, moduleDir string) (Scope, error) {
	topLevel = resolveSymlinks(filepath.FromSlash(topLevel))
	moduleDir = resolveSymlinks(moduleDir)

	rel, err := filepath.Rel(topLevel, moduleDir)
	if err != nil {
		return Scope{}, fmt.Errorf("discover: %w", err)
	}

	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" {
		return Scope{}, nil
	}
	if len(rel) >= 2 && rel[:2] == ".." {
		return Scope{}, fmt.Errorf("discover: module directory %s is not inside the repository %s", moduleDir, topLevel)
	}

	return Scope{Dir: rel, Prefix: rel + "/"}, nil
}

// MatchesTag reports whether tag names a version release in this scope: it
// carries Prefix, and what follows looks like "vN...". It returns the part
// after Prefix, since that is what every caller needs next — as a version to
// parse, or a version to reattach Prefix to.
func (s Scope) MatchesTag(tag string) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(tag, s.Prefix)
	if !ok {
		return "", false
	}
	if len(rest) < 2 || rest[0] != 'v' || rest[1] < '0' || rest[1] > '9' {
		return "", false
	}
	return rest, true
}

// LatestTag picks the highest version among tags that are in this scope, and
// returns its full tag name (Prefix included). ok is false when none of them
// are: an unscoped repository with no releases yet, or a monorepo module
// whose own prefix matches nothing.
func (s Scope) LatestTag(tags []string) (tag string, ok bool) {
	fullTag := make(map[string]string, len(tags))
	versions := make([]string, 0, len(tags))
	for _, t := range tags {
		rest, matched := s.MatchesTag(t)
		if !matched {
			continue
		}
		versions = append(versions, rest)
		fullTag[rest] = t
	}

	tag, ok = fullTag[semver.Latest(versions)]
	return tag, ok
}

// resolveSymlinks returns dir with its symlinks resolved, or dir itself if
// that fails — a directory git and FindModule already found is worth
// comparing even when it cannot be canonicalized further.
func resolveSymlinks(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}
