package feature_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/feature"
)

// A catalogue entry nothing consults is a promise the tool does not keep: the
// directive validates, `letsgo features` lists it, and the release ignores it.
// So every feature a repository can switch (disable or require) must be named
// in the code that acts on it, and every feature with an enabling directive
// must have that directive known to the config.
func TestEveryFeatureIsConsulted(t *testing.T) {
	root := filepath.Join("..", "..")
	sources := productionSources(t, root)

	for _, f := range feature.All {
		if f.Disable || f.Require {
			literal := `"` + f.Name + `"`
			if !slices.ContainsFunc(sources, func(s string) bool { return strings.Contains(s, literal) }) {
				t.Errorf("feature %q can be disabled or required, but no code outside the catalogue names it", f.Name)
			}
		}
		if f.Enable != "" && !slices.Contains(config.Directives(), f.Enable) {
			t.Errorf("feature %q is enabled by %q, which is not a known directive", f.Name, f.Enable)
		}
	}
}

// productionSources reads every non-test Go file that acts on a feature. The
// catalogue itself, the listing that prints it and the config that validates
// names against it only echo a name back, so they cannot count as consulting it.
func productionSources(t *testing.T, root string) []string {
	t.Helper()
	skip := []string{
		filepath.Join("internal", "feature"),
		filepath.Join("internal", "config"),
		filepath.Join("cmd", "letsgo", "features.go"),
	}
	var sources []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, s := range skip {
			if rel == s || strings.HasPrefix(rel, s+string(filepath.Separator)) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources = append(sources, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sources
}
