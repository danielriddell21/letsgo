package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

func TestParseRemote(t *testing.T) {
	want := Repo{Host: "github.com", Owner: "danielriddell21", Name: "letsgo"}

	urls := []string{
		"https://github.com/danielriddell21/letsgo",
		"https://github.com/danielriddell21/letsgo.git",
		"https://github.com/danielriddell21/letsgo/",
		"http://github.com/danielriddell21/letsgo.git",
		"git@github.com:danielriddell21/letsgo.git",
		"git@github.com:danielriddell21/letsgo",
		"ssh://git@github.com/danielriddell21/letsgo.git",
		"https://token@github.com/danielriddell21/letsgo.git",
		"https://user:pass@github.com/danielriddell21/letsgo.git",
		"ssh://git@github.com:22/danielriddell21/letsgo.git",
		"  https://github.com/danielriddell21/letsgo.git  ",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			got, err := ParseRemote(url)
			if err != nil {
				t.Fatalf("ParseRemote: %v", err)
			}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

// A remote carrying a token must not leak it into anything we derive from it.
func TestParseRemoteDropsCredentials(t *testing.T) {
	got, err := ParseRemote("https://ghp_secrettoken@github.com/you/foo.git")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.String(), "ghp_") {
		t.Errorf("credential survived parsing: %s", got)
	}
}

func TestParseRemoteRejectsGarbage(t *testing.T) {
	for _, url := range []string{"", "not-a-url", "https://github.com/only-owner"} {
		if _, err := ParseRemote(url); err == nil {
			t.Errorf("ParseRemote(%q) succeeded, want an error", url)
		}
	}
}

func TestInspectVars(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

const buildID = "const-value"

var (
	version   string
	commit    = "none"
	buildDate = currentDate()
	retries   int
	Version   = "capitalised"
)

func currentDate() string { return "" }

func main() {}
`)
	// A test file must not contribute symbols: it is not linked into the
	// released binary.
	write(t, dir, "main_test.go", `package main

var onlyInTests = "should not be found"
`)

	names := []string{"version", "commit", "buildDate", "retries", "buildID", "missing", "verison", "onlyInTests"}
	got, err := InspectVars(dir, names)
	if err != nil {
		t.Fatalf("InspectVars: %v", err)
	}

	byName := map[string]Symbol{}
	for _, s := range got {
		byName[s.Name] = s
	}

	want := map[string]SymbolStatus{
		"version":     SymbolOK,
		"commit":      SymbolOK,
		"buildDate":   SymbolDynamicInit,
		"retries":     SymbolNotString,
		"buildID":     SymbolIsConst,
		"missing":     SymbolMissing,
		"verison":     SymbolMissing,
		"onlyInTests": SymbolMissing,
	}

	for name, wantStatus := range want {
		if got := byName[name].Status; got != wantStatus {
			t.Errorf("%s: status = %q, want %q (detail: %s)", name, got, wantStatus, byName[name].Detail)
		}
	}

	// The overwhelmingly common form of this mistake is a case difference.
	if s := byName["verison"]; s.Suggestion == "" {
		t.Error("verison: expected a suggestion")
	}
	if s := byName["version"]; !s.OK() {
		t.Error("version should be usable")
	}
}

func TestFindMainPackages(t *testing.T) {
	t.Run("cmd directory wins", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		write(t, dir, "foo.go", "package foo\n")
		write(t, dir, "cmd/alpha/main.go", "package main\nfunc main() {}\n")
		write(t, dir, "cmd/beta/main.go", "package main\nfunc main() {}\n")
		write(t, dir, "cmd/notacommand/lib.go", "package notacommand\n")

		got, err := FindMainPackages(dir, "foo")
		if err != nil {
			t.Fatalf("FindMainPackages: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("found %d packages, want 2: %+v", len(got), got)
		}
		// Sorted, so the order is stable across filesystems.
		if got[0].RelPath != "./cmd/alpha" || got[1].RelPath != "./cmd/beta" {
			t.Errorf("got %q and %q", got[0].RelPath, got[1].RelPath)
		}
		if got[0].BinaryName != "alpha" {
			t.Errorf("BinaryName = %q, want alpha", got[0].BinaryName)
		}
	})

	t.Run("module root", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		write(t, dir, "main.go", "package main\nfunc main() {}\n")

		got, err := FindMainPackages(dir, "foo")
		if err != nil {
			t.Fatalf("FindMainPackages: %v", err)
		}
		if len(got) != 1 || got[0].RelPath != "." || got[0].BinaryName != "foo" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("library has no commands", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		write(t, dir, "foo.go", "package foo\n")

		if _, err := FindMainPackages(dir, "foo"); err == nil {
			t.Error("FindMainPackages succeeded for a library, want an error")
		}
	})
}

func TestFindGit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")
	run("remote", "add", "origin", "git@github.com:you/foo.git")

	ctx := context.Background()

	g, err := FindGit(ctx, dir)
	if err != nil {
		t.Fatalf("FindGit: %v", err)
	}
	if !g.Clean {
		t.Error("worktree should be clean")
	}
	if len(g.Tags) != 1 || g.Tags[0] != "v1.0.0" {
		t.Errorf("Tags = %v, want [v1.0.0]", g.Tags)
	}
	if g.CommitTime.UTC().Format("2006-01-02T15:04:05Z") != "2024-03-15T12:30:45Z" {
		t.Errorf("CommitTime = %s, want the committer date", g.CommitTime)
	}
	if len(g.Commit) != 40 {
		t.Errorf("Commit = %q, want a full sha", g.Commit)
	}
	if !strings.HasPrefix(g.Commit, g.ShortCommit) {
		t.Errorf("ShortCommit %q is not a prefix of %q", g.ShortCommit, g.Commit)
	}
	if g.Shallow {
		t.Error("a fresh repository should not be shallow")
	}

	repo, err := FindRepo(ctx, dir)
	if err != nil {
		t.Fatalf("FindRepo: %v", err)
	}
	if repo.Owner != "you" || repo.Name != "foo" {
		t.Errorf("repo = %+v", repo)
	}

	// A tag on HEAD is the release being made, not the one before it.
	if prev, err := PreviousTag(ctx, dir); err != nil || prev != "" {
		t.Errorf("PreviousTag = %q, %v; want empty for a first release", prev, err)
	}

	write(t, dir, "README.md", "changed\n")
	dirty, err := FindGit(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Clean {
		t.Error("worktree should be dirty after an edit")
	}

	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "v1.1.0")

	if prev, err := PreviousTag(ctx, dir); err != nil || prev != "v1.0.0" {
		t.Errorf("PreviousTag = %q, %v; want v1.0.0", prev, err)
	}
}
