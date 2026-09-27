package discover

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
)

// NestedModuleDirs returns the directories of every Go module nested inside
// dir, relative to dir and slash-separated. dir's own go.mod, if it has one,
// is not included.
//
// Each nested module Go already versions independently, by its own tags
// under its own directory, so its commits are not evidence for dir's own
// release: a commit that only touched a nested module's files says nothing
// about whether dir itself changed.
func NestedModuleDirs(dir string) ([]string, error) {
	var nested []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}

		modDir := filepath.Dir(path)
		if modDir == dir {
			return nil
		}

		rel, err := filepath.Rel(dir, modDir)
		if err != nil {
			return fmt.Errorf("discover: %w", err)
		}
		nested = append(nested, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover: walking %s for nested modules: %w", dir, err)
	}

	sort.Strings(nested)
	return nested, nil
}
