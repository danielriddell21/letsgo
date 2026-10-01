package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// demoMainGo is a program small enough to build in a test, but one that
// answers --version: planAndBuild's smoke test insists on that from anything
// it builds.
const demoMainGo = `package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("demo %s (%s) built %s\n", version, commit, date)
	}
}
`

// moduleFixture writes a minimal buildable module and commits it, so
// plan.Resolve has a real repository to work from.
//
// Driven as one shell-independent command list, with the identity given as
// -c flags rather than a GIT_AUTHOR_* environment: a test fixture belongs to
// this file, not copied from the shape another package's already has.
func moduleFixture(t *testing.T) string {
	t.Helper()
	return moduleFixtureWith(t, "", nil)
}

// moduleFixtureWith is moduleFixture with directives appended to its
// letsgo.mod and extra files committed beside it.
func moduleFixtureWith(t *testing.T, config string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    demoMainGo,
		"letsgo.mod": "build " + gobuild.Host().String() + "\n" + config,
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	identity := []string{"-c", "user.name=Test", "-c", "user.email=t@example.com"}
	for _, args := range [][]string{
		{"-C", dir, "init", "-q", "-b", "main"},
		{"-C", dir, "remote", "add", "origin", "https://github.com/you/demo.git"},
		{"-C", dir, "add", "."},
		append(append([]string{"-C", dir}, identity...), "commit", "-q", "-m", "feat: first release"),
		{"-C", dir, "tag", "v1.2.3"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// A release reads the repository's description through the same Describe
// callback runRelease wires up, and the result is what the caller gets back
// to hand to publishTap — the whole point of threading it through Build.
func TestPlanAndBuildRunsDescribeAndReturnsItsResult(t *testing.T) {
	dir := moduleFixture(t)
	want := &github.RepoInfo{Description: "a demo"}
	var described *plan.Plan

	p, outDir, result, info, err := planAndBuild(context.Background(), planBuildOptions{
		Out:         filepath.Join(t.TempDir(), "dist"),
		Plan:        plan.Options{Dir: dir},
		FailureNote: "nothing was built",
		Started:     time.Now(),
		Describe: func(p *plan.Plan) *github.RepoInfo {
			described = p
			return want
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info != want {
		t.Errorf("info = %v, want %v", info, want)
	}
	if described != p {
		t.Error("Describe was not called with the resolved plan")
	}
	if outDir == "" || result == nil {
		t.Errorf("outDir = %q, result = %v", outDir, result)
	}
}

// `letsgo build` sets no Describe, and a release should not read the
// repository over that alone.
func TestPlanAndBuildSkipsDescribeWhenUnset(t *testing.T) {
	dir := moduleFixture(t)

	_, _, _, info, err := planAndBuild(context.Background(), planBuildOptions{
		Out:         filepath.Join(t.TempDir(), "dist"),
		Plan:        plan.Options{Dir: dir},
		FailureNote: "nothing was built",
		Started:     time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Errorf("info = %v, want nil", info)
	}
}

// A plan that fails its own gates never reaches Build, or Describe.
func TestPlanAndBuildFailsWhenThePlanDoes(t *testing.T) {
	dir := t.TempDir() // no module here at all: plan.Resolve cannot succeed.

	described := false
	_, _, _, _, err := planAndBuild(context.Background(), planBuildOptions{
		Out:         filepath.Join(t.TempDir(), "dist"),
		Plan:        plan.Options{Dir: dir},
		FailureNote: "nothing was built",
		Started:     time.Now(),
		Describe:    func(*plan.Plan) *github.RepoInfo { described = true; return nil },
	})
	if err == nil {
		t.Fatal("want an error: there is no module to resolve a plan from")
	}
	if described {
		t.Error("Describe ran despite the plan never resolving")
	}
}

// scopedModuleFixture writes a repository with a nested module, versioned
// under its own directory the way a monorepo tags it — the scenario
// docs/hld/monorepo.md exists for.
func scopedModuleFixture(t *testing.T) (repoDir, moduleDir string) {
	t.Helper()
	repoDir = t.TempDir()
	moduleDir = filepath.Join(repoDir, "services", "api")

	for name, content := range map[string]string{
		"go.mod":                  "module github.com/you/foo\n\ngo 1.24\n",
		"services/api/go.mod":     "module github.com/you/foo/services/api\n\ngo 1.24\n",
		"services/api/letsgo.mod": "build " + gobuild.Host().String() + "\n",
	} {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Set locally rather than passed as -c on each command: runTag makes its
	// own git calls against this repository (discover.CreateTag among them),
	// and those need an identity too, not just the ones this fixture runs
	// itself.
	for _, args := range [][]string{
		{"-C", repoDir, "init", "-q", "-b", "main"},
		{"-C", repoDir, "config", "user.name", "Test"},
		{"-C", repoDir, "config", "user.email", "t@example.com"},
		{"-C", repoDir, "add", "."},
		{"-C", repoDir, "commit", "-q", "-m", "first"},
		{"-C", repoDir, "tag", "services/api/v1.2.3"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repoDir, moduleDir
}

// `letsgo tag`, run from a nested module's own directory, proposes and
// creates a tag scoped to it — its own directory as a prefix, not a bare
// version tag that would collide with the repository's own scope.
func TestRunTagCreatesAScopedTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	if err := runTag([]string{"--yes"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	// The fixture's HEAD is already tagged services/api/v1.2.3, so that tag
	// itself is excluded as "the release being made"; with nothing else
	// reachable, this is a first release for the previous tag to find, and
	// bump.Propose's own answer for that is v0.1.0.
	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v0.1.0") {
		t.Errorf("tags at HEAD = %q, want services/api/v0.1.0 among them", out)
	}
}

// --pre proposes a prerelease instead of a stable version: the same base
// bump.Propose computes, with an auto-numbered "-rc.N" suffix.
func TestRunTagCreatesAPrereleaseTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	if err := runTag([]string{"--yes", "--pre"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v0.1.0-rc.1") {
		t.Errorf("tags at HEAD = %q, want services/api/v0.1.0-rc.1", out)
	}
}

// commitAndTag writes a file, commits it, and (if tag is non-empty) tags the
// commit — the shared pattern behind every test that moves HEAD past an
// existing tag before letting runTag propose from a fresh, untagged commit.
func commitAndTag(t *testing.T, repoDir, file, tag string) {
	t.Helper()
	path := filepath.Join(repoDir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repoDir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", full, err, out)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "commit "+file)
	if tag != "" {
		run("tag", tag)
	}
}

// A repeated --pre run against the same base advances rc.1, rc.2, ...
// instead of colliding on the same candidate tag.
func TestRunTagIncrementsAnExistingPrerelease(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)

	// The fixture already tagged HEAD as the stable services/api/v1.2.3; move
	// past it, tag a first prerelease of the next patch, then move past that
	// too so runTag proposes from an untagged HEAD again.
	commitAndTag(t, repoDir, "services/api/a.txt", "services/api/v1.2.4-rc.1")
	commitAndTag(t, repoDir, "services/api/b.txt", "")

	t.Chdir(moduleDir)
	if err := runTag([]string{"--yes", "--patch", "--pre"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v1.2.4-rc.2") {
		t.Errorf("tags at HEAD = %q, want services/api/v1.2.4-rc.2", out)
	}
}

// A proposal must skip an intervening prerelease and bump from the last
// stable: without --pre, "previous" is always "the highest stable release,
// full stop," not whatever tag git describe happens to be nearest to.
func TestRunTagSkipsAnInterveningPrereleaseTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)

	// The fixture already tagged HEAD as the stable services/api/v1.2.3; move
	// past it with a higher prerelease, then an untagged commit for runTag to
	// propose from.
	commitAndTag(t, repoDir, "services/api/a.txt", "services/api/v1.3.0-rc.1")
	commitAndTag(t, repoDir, "services/api/b.txt", "")

	t.Chdir(moduleDir)
	if err := runTag([]string{"--yes", "--patch"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v1.2.4") {
		t.Errorf("tags at HEAD = %q, want services/api/v1.2.4 (a patch of the last stable, not the rc)", out)
	}
}

// `tag --json` is a dry run: it prints the proposal and creates nothing, so
// an editor can show it without the write `--yes` performs.
func TestRunTagPrintsJSONWhenRequestedAndCreatesNoTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	out := captureStdout(t, func() {
		if err := runTag([]string{"--json"}); err != nil {
			t.Fatalf("runTag: %v", err)
		}
	})

	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runTag --json output = %q, want it to contain a schema field", out)
	}

	tags, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, tags)
	}
	if strings.Contains(string(tags), "v0.1.0") {
		t.Errorf("tags at HEAD = %q, want no new tag from a --json dry run", tags)
	}
}

// A dirty worktree blocks a real tag (a tag names a commit, so uncommitted
// work has to be dealt with first), but --json is read-only and an editor
// needs it to keep working while a file is being edited.
func TestRunTagJSONWorksWithUncommittedChanges(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(moduleDir)

	out := captureStdout(t, func() {
		if err := runTag([]string{"--json"}); err != nil {
			t.Fatalf("runTag: %v", err)
		}
	})

	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runTag --json output = %q, want it to contain a schema field", out)
	}
}

// A worktree always checks out the whole repository, so a nested module has
// to be compared at <worktree>/relDir, never at the worktree's own root: the
// same bug `plan.checkoutTag` had, in the command that proposes a tag rather
// than the one that resolves one.
// An unknown --format is rejected before the two sides are even resolved,
// so a typo doesn't cost a network round trip.
func TestRunDiffRejectsAnUnknownFormat(t *testing.T) {
	err := unwired.runDiff([]string{"--format", "yaml", "a.json", "b.json"})
	if err == nil || !strings.Contains(err.Error(), `unknown --format "yaml"`) {
		t.Fatalf("err = %v, want an unknown --format error", err)
	}
}

func writeManifest(t *testing.T, dir, name, version, goVersion string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := json.Marshal(manifest.Manifest{
		Schema:  manifest.Schema,
		Version: version,
		Builder: manifest.Builder{Tool: "letsgo", Go: goVersion},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// captureStdout runs fn with os.Stdout redirected, and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = stdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

func TestRunDiffPrintsEachFormat(t *testing.T) {
	dir := t.TempDir()
	from := writeManifest(t, dir, "from.json", "v1.0.0", "go1.26.1")
	to := writeManifest(t, dir, "to.json", "v1.1.0", "go1.26.2")

	for _, tt := range []struct {
		format string
		want   string
	}{
		{"text", "v1.0.0 -> v1.1.0"},
		{"md", "go1.26.1 → go1.26.2"},
		{"json", `"schema": 1`},
	} {
		var runErr error
		out := captureStdout(t, func() {
			runErr = unwired.runDiff([]string{"--format", tt.format, from, to})
		})
		if runErr != nil {
			t.Fatalf("runDiff --format %s: %v", tt.format, runErr)
		}
		if !strings.Contains(out, tt.want) {
			t.Errorf("--format %s stdout = %q, want it to contain %q", tt.format, out, tt.want)
		}
	}
}

func TestCheckoutForDiffReturnsTheModulesOwnDirectory(t *testing.T) {
	repoDir, _ := scopedModuleFixture(t)

	old, cleanup, err := checkoutForDiff(context.Background(), repoDir, "services/api/v1.2.3", "services/api")
	if err != nil {
		t.Fatalf("checkoutForDiff: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(old, "go.mod"))
	if err != nil {
		t.Fatalf("the returned directory is not the nested module's own: %v", err)
	}
	if string(data) != "module github.com/you/foo/services/api\n\ngo 1.24\n" {
		t.Errorf("go.mod = %q, want the nested module's own", data)
	}
}

// scopePrefix resolves the module's own scope prefix, so verify's "no tag
// given" can stay inside it instead of picking another module's release.
func TestScopePrefixResolvesTheModulesOwnPrefix(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)

	prefix, err := scopePrefix(context.Background(), "", moduleDir)
	if err != nil {
		t.Fatalf("scopePrefix: %v", err)
	}
	if prefix != "services/api/" {
		t.Errorf("prefix = %q, want services/api/", prefix)
	}
}

// A repository named explicitly by --repo has no local module to scope by:
// inspecting a release elsewhere always means the whole repository.
func TestScopePrefixIsEmptyWhenRepoIsExplicit(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)

	prefix, err := scopePrefix(context.Background(), "you/elsewhere", moduleDir)
	if err != nil {
		t.Fatalf("scopePrefix: %v", err)
	}
	if prefix != "" {
		t.Errorf("prefix = %q, want empty for an explicit --repo", prefix)
	}
}

// A directory outside any git repository has no scope to resolve, and that
// has to surface as an error rather than a silently unscoped lookup.
func TestScopePrefixFailsOutsideAGitRepository(t *testing.T) {
	dir := t.TempDir()

	if _, err := scopePrefix(context.Background(), "", dir); err == nil {
		t.Error("scopePrefix succeeded outside a git repository")
	}
}

// bump.Propose parses the previous tag as a plain "vX.Y.Z"; a module scoped
// under services/api carries that version behind a "services/api/" prefix,
// which has to come off before proposeVersion hands it to bump.Propose, or
// every scoped tag proposal fails outright.
func TestProposeVersionStripsThePrefixBeforeParsingSemver(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)
	module := discover.Module{Path: "github.com/you/foo/services/api", Dir: moduleDir}
	scope := discover.Scope{Dir: "services/api", Prefix: "services/api/"}

	proposal, err := proposeVersion(context.Background(), module, scope, "services/api/v1.2.3", bump.None)
	if err != nil {
		t.Fatalf("proposeVersion: %v", err)
	}
	if proposal.Next != "v1.2.4" {
		t.Errorf("Next = %q, want v1.2.4 (a patch bump of the stripped version)", proposal.Next)
	}
}

// A commit that only touched a further-nested module (one inside the module
// being tagged) is not evidence for this module's own bump: it belongs to a
// release with its own history.
func TestProposeVersionExcludesANestedModulesOwnCommits(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "t@example.com")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")

	write("plugin/go.mod", "module github.com/you/foo/plugin\n")
	run("add", ".")
	run("commit", "-q", "-m", "feat!: nested module's own breaking change")

	module := discover.Module{Path: "github.com/you/foo", Dir: dir}
	proposal, err := proposeVersion(context.Background(), module, discover.Scope{}, "v1.0.0", bump.None)
	if err != nil {
		t.Fatalf("proposeVersion: %v", err)
	}
	if proposal.Next != "v1.0.1" {
		t.Errorf("Next = %q, want v1.0.1: the nested module's commit must not count as a signal here", proposal.Next)
	}
}

// flagSet mirrors the shapes the real subcommands declare: a boolean, a
// string, and a second string, so permutation is tested against flags that
// differ in whether they take a value.
func flagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("yes", false, "")
	fs.String("reason", "", "")
	fs.String("token", "", "")
	return fs
}

// A release only reads the repository's description when there is a
// Homebrew tap to write into — a formula's, or a tap-files plugin's cask.
func TestWantsRepoInfo(t *testing.T) {
	tap := github.Repo{Owner: "you", Name: "homebrew-tap"}

	for _, tc := range []struct {
		name string
		p    *plan.Plan
		want bool
	}{
		{"no tap", &plan.Plan{HasRepo: true}, false},
		{"tap but no repository", &plan.Plan{Tap: tap}, false},
		{"tap and repository", &plan.Plan{Tap: tap, HasRepo: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsRepoInfo(tc.p); got != tc.want {
				t.Errorf("wantsRepoInfo(%+v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestRepoInfoForRunsDescribeWhenSet(t *testing.T) {
	want := &github.RepoInfo{Description: "a thing"}
	o := planBuildOptions{Describe: func(*plan.Plan) *github.RepoInfo { return want }}

	if got := repoInfoFor(o, &plan.Plan{}); got != want {
		t.Errorf("repoInfoFor = %v, want %v", got, want)
	}
}

// `letsgo build` never touches the network, so it sets no Describe at all.
func TestRepoInfoForNilWhenUnset(t *testing.T) {
	if got := repoInfoFor(planBuildOptions{}, &plan.Plan{}); got != nil {
		t.Errorf("repoInfoFor = %v, want nil", got)
	}
}

func TestPermuteMovesFlagsAhead(t *testing.T) {
	tests := map[string]struct {
		in   []string
		want []string
	}{
		"already in order":   {[]string{"--reason", "x", "v1.2.3"}, []string{"--reason", "x", "v1.2.3"}},
		"flag after operand": {[]string{"v1.2.3", "--reason", "x"}, []string{"--reason", "x", "v1.2.3"}},
		"boolean after":      {[]string{"v1.2.3", "--yes"}, []string{"--yes", "v1.2.3"}},
		"attached value":     {[]string{"v1.2.3", "--reason=x"}, []string{"--reason=x", "v1.2.3"}},
		"single dash":        {[]string{"v1.2.3", "-yes"}, []string{"-yes", "v1.2.3"}},
		"two operands":       {[]string{"a.json", "b.json"}, []string{"a.json", "b.json"}},
		"interleaved":        {[]string{"a", "--yes", "b", "--reason", "x"}, []string{"--yes", "--reason", "x", "a", "b"}},
		"nothing":            {nil, nil},
	}

	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			got := permute(flagSet(), c.in)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("permute(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// A boolean flag must not swallow the argument after it, or `letsgo yank --yes
// v1.2.3` would lose its tag.
func TestPermuteKeepsBooleansFromEatingOperands(t *testing.T) {
	fs := flagSet()
	if err := fs.Parse(permute(fs, []string{"--yes", "v1.2.3"})); err != nil {
		t.Fatal(err)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "v1.2.3" {
		t.Errorf("args = %v", fs.Args())
	}
}

// Everything after "--" is an operand by definition, however it is spelled.
func TestPermuteRespectsTheTerminator(t *testing.T) {
	got := permute(flagSet(), []string{"--yes", "--", "--not-a-flag", "x"})

	want := []string{"--yes", "--", "--not-a-flag", "x"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("permute = %v, want %v", got, want)
	}
}

// An unknown flag must reach the flag package to be reported, rather than
// quietly consuming the operand behind it.
func TestPermuteLeavesUnknownFlagsAlone(t *testing.T) {
	fs := flagSet()
	fs.SetOutput(discard{})

	if err := fs.Parse(permute(fs, []string{"v1.2.3", "--nonsense"})); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Tags do not exist on disk, so the file system decides which side of a diff
// is which.
func TestIsManifestPath(t *testing.T) {
	if isManifestPath("") || isManifestPath("v1.2.3") {
		t.Error("a tag was mistaken for a path")
	}
	if isManifestPath(t.TempDir()) {
		t.Error("a directory was mistaken for a manifest")
	}

	path := t.TempDir() + "/letsgo.json"
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isManifestPath(path) {
		t.Error("a file was not recognised")
	}
}

// Rounding everything to tenths reports a plan that finished in forty
// milliseconds as "0s", which reads like the tool did nothing.
func TestTookKeepsSubSecondDetail(t *testing.T) {
	if got := took(time.Now().Add(-40 * time.Millisecond)); got == 0 {
		t.Errorf("took = %v, want a measurable duration", got)
	}
	if got := took(time.Now().Add(-90 * time.Second)); got.Round(time.Second) != 90*time.Second {
		t.Errorf("took = %v", got)
	}
}

func TestErrUsage(t *testing.T) {
	if err := errUsage("letsgo yank <tag>"); !strings.HasPrefix(err.Error(), "usage: ") {
		t.Errorf("errUsage = %v", err)
	}
}

func TestFileSum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileSum(path)
	want := sha256.Sum256([]byte("x"))
	if err != nil || !bytes.Equal(got, want[:]) {
		t.Errorf("fileSum = %x, %v, want %x", got, err, want)
	}
	if _, err := fileSum(path + ".missing"); err == nil {
		t.Error("fileSum of a missing file succeeded")
	}
}

// With no "to", diff compares against the latest release within the module's
// own scope, not another module's.
func TestRunDiffDefaultsToTheModulesOwnLatestRelease(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	if out, err := exec.Command("git", "-C", repoDir, "remote", "add", "origin", "https://github.com/you/foo.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/you/foo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "other/v9.0.0"})
	})
	mux.HandleFunc("/repos/you/foo/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "other/v9.0.0"}, {Name: "services/api/v1.2.3"}})
	})
	mux.HandleFunc("/repos/you/foo/releases/tags/services/api/v1.2.3", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{
			ID: 2, TagName: "services/api/v1.2.3",
			Assets: []github.Asset{{ID: 5, Name: manifest.FileName}},
		})
	})
	mux.HandleFunc("/repos/you/foo/releases/assets/5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schema":1,"version":"1.2.3"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f := forge{endpoint: srv.URL}

	t.Chdir(moduleDir)
	local := filepath.Join(t.TempDir(), "letsgo.json")
	if err := os.WriteFile(local, []byte(`{"schema":1,"version":"1.2.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.runDiff([]string{local}); err != nil {
		t.Fatalf("runDiff: %v", err)
	}
}

// A release goes through the injected forge from the first gate to the last
// upload: the plan checks write access, the release is created, every built
// file is attached, and nothing reaches an endpoint the fake does not serve.
func TestRunReleasePublishesThroughTheInjectedForge(t *testing.T) {
	ff, f := newFakeForge(t)
	t.Chdir(moduleFixture(t))

	var err error
	out := captureStdout(t, func() {
		err = f.runRelease([]string{"-no-proxy-warm", "-o", filepath.Join(t.TempDir(), "dist")})
	})
	if err != nil {
		t.Fatalf("runRelease = %v\n%s", err, out)
	}

	if missed := ff.unhandled(); len(missed) > 0 {
		t.Errorf("requests the forge does not serve: %v", missed)
	}
	rel := ff.release("v1.2.3")
	if rel == nil {
		t.Fatalf("no release was published\n%s", out)
	}
	if rel.Draft || rel.Prerelease {
		t.Errorf("release = draft %v, prerelease %v; want a stable release", rel.Draft, rel.Prerelease)
	}
	if len(rel.Assets) == 0 {
		t.Fatalf("release has no assets\n%s", out)
	}
	if body, ok := ff.asset("v1.2.3", manifest.FileName); !ok || len(body) == 0 {
		t.Errorf("the manifest was not uploaded; assets = %v", rel.Assets)
	}
	if !strings.Contains(out, "released in") || !strings.Contains(out, rel.HTMLURL) {
		t.Errorf("output does not report the release:\n%s", out)
	}
}

// A release the forge does not have is a failed verification, reported as an
// error rather than a hang or a panic: the run gets as far as the forge.
func TestRunVerifyReportsAMissingRelease(t *testing.T) {
	t.Chdir(moduleFixtureWith(t, "", nil))

	f := emptyForge(t)
	if err := f.runVerify([]string{"-repo", "you/foo", "-no-rebuild", "-work", t.TempDir(), "v9.9.9"}); err == nil {
		t.Error("verifying a release that does not exist succeeded")
	}
}
