package discover

import (
	"fmt"
	"path/filepath"
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
func NewScope(topLevel, moduleDir string) (Scope, error) {
	rel, err := filepath.Rel(filepath.FromSlash(topLevel), moduleDir)
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
