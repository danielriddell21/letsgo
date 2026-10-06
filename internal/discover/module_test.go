package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModulePath(t *testing.T) {
	cases := map[string]struct {
		goMod string
		want  string
	}{
		"plain":            {"module example.com/foo\n\ngo 1.24\n", "example.com/foo"},
		"quoted":           {"module \"example.com/foo\"\n", "example.com/foo"},
		"trailing comment": {"module example.com/foo // a comment\n", "example.com/foo"},
		"leading blank":    {"\n\n\nmodule example.com/foo\n", "example.com/foo"},
		"block form":       {"module (\n\texample.com/foo\n)\n", "example.com/foo"},
		"tabbed":           {"module\texample.com/foo\n", "example.com/foo"},
		"comment first":    {"// module example.com/wrong\nmodule example.com/foo\n", "example.com/foo"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "go.mod", tc.goMod)

			m, err := FindModule(dir)
			if err != nil {
				t.Fatalf("FindModule: %v", err)
			}
			if m.Path != tc.want {
				t.Errorf("Path = %q, want %q", m.Path, tc.want)
			}
		})
	}
}

// A directive whose name merely starts with "module" must not be mistaken for
// the module directive.
func TestModulePathIgnoresSimilarDirectives(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "modules example.com/wrong\nmodule example.com/right\n")

	m, err := FindModule(dir)
	if err != nil {
		t.Fatalf("FindModule: %v", err)
	}
	if m.Path != "example.com/right" {
		t.Errorf("Path = %q, want example.com/right", m.Path)
	}
}

func TestGoDirective(t *testing.T) {
	cases := map[string]struct {
		goMod     string
		want      string
		wantExact bool
	}{
		"two-part go directive":     {"module example.com/foo\n\ngo 1.24\n", "go1.24", false},
		"three-part go directive":   {"module example.com/foo\n\ngo 1.24.7\n", "go1.24.7", false},
		"toolchain wins over go":    {"module example.com/foo\n\ngo 1.24\ntoolchain go1.24.7\n", "go1.24.7", true},
		"comment stripped":          {"module example.com/foo\n\ngo 1.24 // a comment\n", "go1.24", false},
		"tabbed":                    {"module example.com/foo\n\ngo\t1.24\n", "go1.24", false},
		"similar directive ignored": {"module example.com/foo\n\ngoversion 1.24\ngo 1.23\n", "go1.23", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "go.mod", tc.goMod)

			got, exact, err := GoDirective(filepath.Join(dir, "go.mod"))
			if err != nil {
				t.Fatalf("GoDirective: %v", err)
			}
			if got != tc.want {
				t.Errorf("GoDirective version = %q, want %q", got, tc.want)
			}
			if exact != tc.wantExact {
				t.Errorf("GoDirective exact = %v, want %v", exact, tc.wantExact)
			}
		})
	}
}

func TestGoDirectiveReportsAbsence(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/foo\n")

	if _, _, err := GoDirective(filepath.Join(dir, "go.mod")); err == nil {
		t.Error("want an error when go.mod has no go directive")
	}
}

func TestLocalReplace(t *testing.T) {
	cases := []struct {
		name        string
		goMod       string
		module, dir string
	}{
		{"no replace", "module example.com/foo\n", "", ""},
		{
			"module replacement is not local",
			"module example.com/foo\nreplace example.com/bar => example.com/baz v1.2.3\n", "", "",
		},
		{
			"relative path is local",
			"module example.com/foo\nreplace example.com/bar => ../bar\n", "example.com/bar", "../bar",
		},
		{
			"absolute path is local",
			"module example.com/foo\nreplace example.com/bar => /home/me/bar\n", "example.com/bar", "/home/me/bar",
		},
		{
			"old side carries a version",
			"module example.com/foo\nreplace example.com/bar v1.0.0 => ../bar\n", "example.com/bar", "../bar",
		},
		{
			"block form",
			"module example.com/foo\nreplace (\n\texample.com/bar => ../bar\n)\n", "example.com/bar", "../bar",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "go.mod", c.goMod)

			module, replacement, err := LocalReplace(filepath.Join(dir, "go.mod"))
			if err != nil {
				t.Fatalf("LocalReplace: %v", err)
			}
			if module != c.module || replacement != c.dir {
				t.Errorf("LocalReplace = %q, %q, want %q, %q", module, replacement, c.module, c.dir)
			}
		})
	}
}

func TestFindModuleWalksUpward(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/foo\n")
	nested := filepath.Join(root, "internal", "deep", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := FindModule(nested)
	if err != nil {
		t.Fatalf("FindModule: %v", err)
	}
	if m.Path != "example.com/foo" {
		t.Errorf("Path = %q", m.Path)
	}
	if m.Dir != root {
		t.Errorf("Dir = %q, want %q", m.Dir, root)
	}
}

func TestFindModuleReportsAbsence(t *testing.T) {
	if _, err := FindModule(t.TempDir()); err == nil {
		t.Error("FindModule succeeded with no go.mod, want an error")
	}
}

func TestSplitMajorSuffix(t *testing.T) {
	cases := []struct {
		path      string
		wantName  string
		wantMajor int
	}{
		{"github.com/you/foo", "foo", 0},
		{"github.com/you/foo/v2", "foo", 2},
		{"github.com/you/foo/v17", "foo", 17},
		{"github.com/you/foo/v1", "v1", 0}, // v1 is never a path suffix
		{"github.com/you/foo/v0", "v0", 0}, // nor is v0
		{"github.com/you/version", "version", 0},
		{"example.com/v2", "example.com", 2}, // v2 of module example.com
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			name, major := splitMajorSuffix(tc.path)
			if name != tc.wantName || major != tc.wantMajor {
				t.Errorf("got (%q, %d), want (%q, %d)", name, major, tc.wantName, tc.wantMajor)
			}
		})
	}
}

// The major-version gate. Getting this wrong publishes a release that `go get`
// silently refuses to resolve, with no warning from any Go tool.
func TestModuleCheckTag(t *testing.T) {
	cases := []struct {
		name    string
		module  string
		tag     string
		wantErr bool
	}{
		{"v1 without suffix", "github.com/you/foo", "v1.2.3", false},
		{"v0 without suffix", "github.com/you/foo", "v0.1.0", false},
		{"v2 with suffix", "github.com/you/foo/v2", "v2.0.0", false},
		{"v3 with suffix", "github.com/you/foo/v3", "v3.1.4", false},
		{"prerelease keeps major", "github.com/you/foo/v2", "v2.0.0-rc1", false},

		{"v2 without suffix", "github.com/you/foo", "v2.0.0", true},
		{"v3 with v2 suffix", "github.com/you/foo/v2", "v3.0.0", true},
		{"v1 with v2 suffix", "github.com/you/foo/v2", "v1.9.0", true},
		{"v0 with v2 suffix", "github.com/you/foo/v2", "v0.1.0", true},
		{"not a version", "github.com/you/foo", "release-1", true},
		{"no major digits", "github.com/you/foo", "vX.Y.Z", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, major := splitMajorSuffix(tc.module)
			m := Module{Path: tc.module, Name: name, MajorSuffix: major}

			err := m.CheckTag(tc.tag)
			if tc.wantErr && err == nil {
				t.Errorf("CheckTag(%q) on %q succeeded, want an error", tc.tag, tc.module)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("CheckTag(%q) on %q: %v", tc.tag, tc.module, err)
			}
		})
	}
}

// The error has to say what to do, not just that something is wrong.
func TestMajorVersionErrorIsActionable(t *testing.T) {
	m := Module{Path: "github.com/you/foo", MajorSuffix: 0}
	err := m.CheckTag("v2.0.0")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"/v2", "go get", "retag"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%s", want, err)
		}
	}
}
