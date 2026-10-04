package plugin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/modsyntax"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadConfigLocatesTheFile(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		wantPath string
		wantKey  string
	}{
		{"config dir wins", map[string]string{".letsgo/env.mod": "new x", "letsgo-env.mod": "old x"}, ".letsgo/env.mod", "new"},
		{"legacy fallback", map[string]string{"letsgo-env.mod": "old x"}, "letsgo-env.mod", "old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			cfg, err := LoadConfig(filepath.Join(root, ".letsgo"), "env")
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(root, filepath.FromSlash(tt.wantPath)); cfg.Path != want {
				t.Errorf("Path = %q, want %q", cfg.Path, want)
			}
			if len(cfg.Lines) != 1 || cfg.Lines[0].Keyword != tt.wantKey {
				t.Errorf("Lines = %+v, want one %q line", cfg.Lines, tt.wantKey)
			}
		})
	}
}

func TestLoadConfigMissing(t *testing.T) {
	root := writeTree(t, nil)
	_, err := LoadConfig(filepath.Join(root, ".letsgo"), "env")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}

func TestLoadConfigNeedsADir(t *testing.T) {
	if _, err := LoadConfig("", "env"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want a plain error for an empty config dir", err)
	}
}

func TestLoadConfigFlattensBlocks(t *testing.T) {
	root := writeTree(t, map[string]string{".letsgo/env.mod": `// a comment
set A one
set (
	B two
	C "three four"
)
gui variant (
	x
)
`})
	cfg, err := LoadConfig(filepath.Join(root, ".letsgo"), "env")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Keyword string
		Args    []string
		Line    int
	}
	var got []row
	for _, l := range cfg.Lines {
		got = append(got, row{l.Keyword, l.Args, l.Pos.Line})
	}
	want := []row{
		{"set", []string{"A", "one"}, 2},
		{"set", []string{"B", "two"}, 4},
		{"set", []string{"C", "three four"}, 5},
		{"gui", []string{"variant", "x"}, 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %+v, want %+v", got, want)
	}
}

func TestLoadConfigReportsSyntaxErrorsWithThePath(t *testing.T) {
	root := writeTree(t, map[string]string{".letsgo/env.mod": "set (\n\tA b\n"})
	_, err := LoadConfig(filepath.Join(root, ".letsgo"), "env")
	var se *modsyntax.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *modsyntax.SyntaxError", err)
	}
	if !strings.HasPrefix(err.Error(), filepath.Join(root, ".letsgo", "env.mod")+":") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestConfigErrorfNamesTheLine(t *testing.T) {
	cfg := &Config{Path: "x/env.mod"}
	err := cfg.Errorf(ConfigLine{Pos: modsyntax.Position{Line: 3, Col: 5}}, "bad %s", "thing")
	if err.Error() != "x/env.mod:3:5: bad thing" {
		t.Errorf("err = %q", err)
	}
}
