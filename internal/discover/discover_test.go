package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestNewScope(t *testing.T) {
	top := t.TempDir()

	t.Run("a root module has no scope", func(t *testing.T) {
		s, err := NewScope(top, top)
		if err != nil {
			t.Fatal(err)
		}
		if s != (Scope{}) {
			t.Errorf("Scope = %+v, want empty", s)
		}
	})

	t.Run("a nested module carries its own directory as prefix", func(t *testing.T) {
		nested := filepath.Join(top, "services", "api")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		s, err := NewScope(top, nested)
		if err != nil {
			t.Fatal(err)
		}
		if s.Dir != "services/api" || s.Prefix != "services/api/" {
			t.Errorf("Scope = %+v", s)
		}
	})

	t.Run("a directory outside the repository is refused", func(t *testing.T) {
		if _, err := NewScope(top, t.TempDir()); err == nil {
			t.Error("expected an error for a module outside the repository")
		}
	})

	// git's own --show-toplevel already resolves symlinks (macOS's
	// /tmp -> /private/tmp, among others), so a caller comparing against its
	// own, unresolved directory must not conclude a module is outside its own
	// repository merely because the two spellings disagree.
	t.Run("a symlinked top level still matches the module inside it", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs elevated privileges on Windows")
		}
		actual := filepath.Join(t.TempDir(), "real")
		if err := os.Mkdir(actual, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(actual, link); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(link, "services", "api")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		s, err := NewScope(link, nested)
		if err != nil {
			t.Fatal(err)
		}
		if s.Dir != "services/api" {
			t.Errorf("Scope = %+v", s)
		}
	})
}

func TestScopeMatchesTag(t *testing.T) {
	cases := []struct {
		name      string
		prefix    string
		tag       string
		wantRest  string
		wantMatch bool
	}{
		{"root scope accepts a plain version", "", "v1.2.3", "v1.2.3", true},
		{"root scope rejects another scope's tag", "", "services/api/v1.0.0", "", false},
		{"nested scope accepts its own tag", "services/api/", "services/api/v1.0.0", "v1.0.0", true},
		{"nested scope rejects the root's tag", "services/api/", "v1.0.0", "", false},
		{"nested scope rejects a sibling's tag", "services/api/", "services/web/v1.0.0", "", false},
		{"a prefix match without a version is not a tag", "services/api/", "services/api/latest", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			rest, ok := scope.MatchesTag(c.tag)
			if ok != c.wantMatch || rest != c.wantRest {
				t.Errorf("MatchesTag(%q) = %q, %v, want %q, %v", c.tag, rest, ok, c.wantRest, c.wantMatch)
			}
		})
	}
}

func TestScopeLatestTag(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		tags    []string
		wantTag string
		wantOK  bool
	}{
		{
			"root scope picks the highest version", "",
			[]string{"v1.0.0", "v2.0.0", "v1.5.0"},
			"v2.0.0", true,
		},
		{
			"nested scope ignores other scopes and the root", "services/api/",
			[]string{"v9.9.9", "services/web/v8.0.0", "services/api/v1.0.0", "services/api/v1.2.3"},
			"services/api/v1.2.3", true,
		},
		{
			"no matching tag has no releases", "services/api/",
			[]string{"v1.0.0", "services/web/v1.0.0"},
			"", false,
		},
		{"no tags at all has no releases", "", nil, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			tag, ok := scope.LatestTag(c.tags)
			if ok != c.wantOK || tag != c.wantTag {
				t.Errorf("LatestTag(%v) = %q, %v, want %q, %v", c.tags, tag, ok, c.wantTag, c.wantOK)
			}
		})
	}
}

func TestScopePreviousTag(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		tags    []string
		tag     string
		wantTag string
		wantOK  bool
	}{
		{
			"a stable release's previous is the highest stable below it, skipping an rc", "services/api/",
			[]string{"services/api/v1.0.0", "services/api/v1.1.0-rc.1", "services/web/v1.0.5", "services/api/v1.1.0"},
			"services/api/v1.1.0",
			"services/api/v1.0.0", true,
		},
		{
			"a prerelease's previous is the highest release of any kind below it", "",
			[]string{"v1.0.0", "v1.1.0-rc.1"},
			"v1.1.0-rc.2",
			"v1.1.0-rc.1", true,
		},
		{
			"a first release has no previous", "",
			[]string{},
			"v1.0.0",
			"", false,
		},
		{
			"a tag outside the scope has no previous", "services/api/",
			[]string{"services/api/v1.0.0"},
			"v2.0.0",
			"", false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			tag, ok := scope.PreviousTag(c.tags, c.tag)
			if ok != c.wantOK || tag != c.wantTag {
				t.Errorf("PreviousTag(%v, %q) = %q, %v, want %q, %v", c.tags, c.tag, tag, ok, c.wantTag, c.wantOK)
			}
		})
	}
}

func TestScopeLatestStableTag(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		tags    []string
		exclude []string
		wantTag string
		wantOK  bool
	}{
		{
			"picks the highest stable, skipping a newer rc", "services/api/",
			[]string{"services/api/v1.0.0", "services/api/v1.1.0-rc.1", "services/web/v9.0.0"},
			nil,
			"services/api/v1.0.0", true,
		},
		{
			"excludes tags pointing at HEAD", "",
			[]string{"v1.0.0", "v1.1.0"},
			[]string{"v1.1.0"},
			"v1.0.0", true,
		},
		{
			"no stable release has no answer", "",
			[]string{"v1.0.0-rc.1"},
			nil,
			"", false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			tag, ok := scope.LatestStableTag(c.tags, c.exclude...)
			if ok != c.wantOK || tag != c.wantTag {
				t.Errorf("LatestStableTag(%v, %v) = %q, %v, want %q, %v", c.tags, c.exclude, tag, ok, c.wantTag, c.wantOK)
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

	// Deliberately misspelled: this is the input the suggestion logic exists
	// to recognise, so it must not be "corrected".
	const typo = "verison" //nolint:misspell // the typo under test

	names := []string{"version", "commit", "buildDate", "retries", "buildID", "missing", typo, "onlyInTests"}
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
		typo:          SymbolMissing,
		"onlyInTests": SymbolMissing,
	}

	for name, wantStatus := range want {
		if got := byName[name].Status; got != wantStatus {
			t.Errorf("%s: status = %q, want %q (detail: %s)", name, got, wantStatus, byName[name].Detail)
		}
	}

	// The overwhelmingly common form of this mistake is a case difference.
	if s := byName[typo]; s.Suggestion == "" {
		t.Errorf("%s: expected a suggestion", typo)
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

// gitRun runs git in dir with a fixed author/committer identity and date, so
// tests built on it are deterministic.
func gitRun(t *testing.T, dir string, args ...string) {
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

func TestFindGit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

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
	if prev, err := PreviousTag(ctx, dir, ""); err != nil || prev != "" {
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

	if prev, err := PreviousTag(ctx, dir, ""); err != nil || prev != "v1.0.0" {
		t.Errorf("PreviousTag = %q, %v; want v1.0.0", prev, err)
	}
}

// Scope depends on this: a root module derived against its own TopLevel must
// come out empty, or every plain repository would carry a prefix.
func TestFindGitPopulatesATopLevelThatAgreesWithItself(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")

	g, err := FindGit(context.Background(), dir)
	if err != nil {
		t.Fatalf("FindGit: %v", err)
	}
	if s, err := NewScope(g.TopLevel, dir); err != nil || s != (Scope{}) {
		t.Errorf("NewScope(TopLevel, dir) = %+v, %v, want an empty scope", s, err)
	}
}

// A nested module's tags (e.g. "web/v1.0.0") must never answer for the root
// module: `git describe --tags` matches any tag reachable from HEAD, so
// without a restriction to plain version tags a root release could pick a
// nested module's tag as its previous release and build its changelog, API
// gate and version bump against the wrong history entirely.
func TestPreviousTagIgnoresPrefixedTags(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")

	write(t, dir, "web/go.mod", "module example.com/foo/web\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "web/v1.0.0")

	write(t, dir, "README.md", "changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")

	ctx := context.Background()
	if prev, err := PreviousTag(ctx, dir, ""); err != nil || prev != "v1.0.0" {
		t.Errorf("PreviousTag = %q, %v; want v1.0.0, not the nested module's tag", prev, err)
	}
}

// Unlike PreviousTag, Tags returns every matching tag reachable from HEAD,
// not just the nearest one, so a version-order rule can be applied on top.
func TestTagsReturnsEveryMatchingTagInScope(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")

	write(t, dir, "web/go.mod", "module example.com/foo/web\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "web/v1.0.0")
	run("tag", "v1.1.0-rc.1")

	write(t, dir, "README.md", "changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")
	run("tag", "v1.1.0")

	ctx := context.Background()
	got, err := Tags(ctx, dir, "")
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	want := map[string]bool{"v1.0.0": true, "v1.1.0-rc.1": true, "v1.1.0": true}
	if len(got) != len(want) {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
	for _, tag := range got {
		if !want[tag] {
			t.Errorf("Tags returned unexpected tag %q (web's tag should have been excluded)", tag)
		}
	}
}

func TestTagsIsEmptyNotAnErrorForAFirstRelease(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")

	got, err := Tags(context.Background(), dir, "")
	if err != nil || got != nil {
		t.Errorf("Tags = %v, %v; want nil, nil", got, err)
	}
}

func TestNestedModuleDirs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/foo\n")
	write(t, dir, "services/api/go.mod", "module example.com/foo/services/api\n")
	write(t, dir, "services/api/internal/tool/go.mod", "module example.com/foo/services/api/tool\n")
	write(t, dir, "web/go.mod", "module example.com/foo/web\n")
	write(t, dir, "web/vendor/other/go.mod", "module example.com/vendored\n")

	got, err := NestedModuleDirs(dir)
	if err != nil {
		t.Fatalf("NestedModuleDirs: %v", err)
	}
	want := []string{"services/api", "services/api/internal/tool", "web"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNestedModuleDirsRelativeToANestedModuleItself(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/foo\n")
	write(t, dir, "services/api/go.mod", "module example.com/foo/services/api\n")
	write(t, dir, "services/api/internal/tool/go.mod", "module example.com/foo/services/api/tool\n")

	got, err := NestedModuleDirs(filepath.Join(dir, "services", "api"))
	if err != nil {
		t.Fatalf("NestedModuleDirs: %v", err)
	}
	if strings.Join(got, ",") != "internal/tool" {
		t.Errorf("got %v, want [internal/tool]", got)
	}
}

// The pathspec Commits builds from exclude must actually exclude, or the
// nested module's own commits leak into a history they should never appear
// in.
func TestCommitsExcludesTheGivenDirectories(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitRun(t, dir, args...) }

	run("init", "-q", "-b", "main")
	write(t, dir, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")

	write(t, dir, "services/api/main.go", "package main\n")
	run("add", ".")
	run("commit", "-q", "-m", "second: touches the nested module only")

	write(t, dir, "README.md", "changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "third: touches the root")

	ctx := context.Background()
	commits, err := Commits(ctx, dir, "", "HEAD", "services/api")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}
	for _, c := range commits {
		if strings.Contains(c.Subject, "second") {
			t.Errorf("the excluded directory's commit was not excluded: %+v", commits)
		}
	}
	if len(commits) != 2 {
		t.Errorf("got %d commits, want 2 (first and third)", len(commits))
	}
}
