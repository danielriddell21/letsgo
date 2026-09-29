package main

import (
	"context"
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

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
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
	dir := t.TempDir()

	for name, content := range map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    demoMainGo,
		"letsgo.mod": "build " + gobuild.Host().String() + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
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
// docs/design/monorepo.md exists for.
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

// A repeated --pre run against the same base advances rc.1, rc.2, ...
// instead of colliding on the same candidate tag.
func TestRunTagIncrementsAnExistingPrerelease(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)

	write := func(name, content string) {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repoDir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", full, err, out)
		}
	}

	// The fixture already tagged HEAD as the stable services/api/v1.2.3; move
	// past it, tag a first prerelease of the next patch, then move past that
	// too so runTag proposes from an untagged HEAD again.
	write("services/api/a.txt", "a\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "services/api/v1.2.4-rc.1")
	write("services/api/b.txt", "b\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")

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

	write := func(name, content string) {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repoDir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", full, err, out)
		}
	}

	// The fixture already tagged HEAD as the stable services/api/v1.2.3; move
	// past it with a higher prerelease, then an untagged commit for runTag to
	// propose from.
	write("services/api/a.txt", "a\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	run("tag", "services/api/v1.3.0-rc.1")
	write("services/api/b.txt", "b\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")

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
	err := runDiff([]string{"--format", "yaml", "a.json", "b.json"})
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
			runErr = runDiff([]string{"--format", tt.format, from, to})
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

func TestReleaseTag(t *testing.T) {
	if got := releaseTag(&plan.Plan{Tag: "v1.2.3", Version: "1.2.3"}); got != "v1.2.3" {
		t.Errorf("releaseTag = %q", got)
	}
	// A rehearsal has no tag, and showing an empty one would misrepresent the
	// call being rehearsed.
	if got := releaseTag(&plan.Plan{Version: "1.2.3-next+abc"}); got != "v1.2.3-next+abc" {
		t.Errorf("releaseTag = %q", got)
	}
}

func TestReleaseTitle(t *testing.T) {
	if got := releaseTitle(&plan.Plan{Tag: "v1.2.3"}); got != "v1.2.3" {
		t.Errorf("releaseTitle for a root module = %q, want the bare tag", got)
	}
	scoped := &plan.Plan{
		Tag:   "services/api/v1.2.3",
		Scope: discover.Scope{Dir: "services/api", Prefix: "services/api/"},
	}
	if got := releaseTitle(scoped); got != "services/api v1.2.3" {
		t.Errorf("releaseTitle for a scoped module = %q, want %q", got, "services/api v1.2.3")
	}
}

func TestIsPrerelease(t *testing.T) {
	tests := map[string]struct {
		version string
		config  string
		want    bool
	}{
		"plain release":       {"1.2.3", "auto", false},
		"prerelease segment":  {"1.2.3-rc.1", "auto", true},
		"build metadata only": {"1.2.3+abc", "auto", false},
		"snapshot":            {"1.2.3-next+abc", "auto", true},
		"forced on":           {"1.2.3", "true", true},
		"forced off":          {"1.2.3-rc.1", "false", false},
		"hyphen in metadata":  {"1.2.3+build-7", "auto", false},
	}

	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			p := &plan.Plan{Version: c.version, Config: &config.Config{Prerelease: c.config}}
			if got := isPrerelease(p); got != c.want {
				t.Errorf("isPrerelease(%q, %q) = %v, want %v", c.version, c.config, got, c.want)
			}
		})
	}
}

func TestIsLatest(t *testing.T) {
	tests := map[string]struct {
		prefix string
		config string
		want   string
	}{
		"root, auto":        {"", "auto", "true"},
		"scoped, auto":      {"services/api/", "auto", "false"},
		"root, forced off":  {"", "false", "false"},
		"scoped, forced on": {"services/api/", "true", "true"},
	}

	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			p := &plan.Plan{
				Scope:  discover.Scope{Prefix: c.prefix},
				Config: &config.Config{Latest: c.config},
			}
			if got := isLatest(p); got != c.want {
				t.Errorf("isLatest(prefix=%q, config=%q) = %q, want %q", c.prefix, c.config, got, c.want)
			}
		})
	}
}

// disable changelog must stop the changelog from being built at all, not
// merely from being shown: a nil client proves this returns before it would
// have made a network call.
func TestReleaseNotesSkippedWhenChangelogDisabled(t *testing.T) {
	p := &plan.Plan{Features: feature.Resolve([]string{"changelog"})}

	notes, err := releaseNotes(context.Background(), p, nil, github.Repo{}, nil)
	if err != nil || notes != "" {
		t.Errorf("releaseNotes = (%q, %v), want empty and no error", notes, err)
	}
}

// manifestForge serves one release whose only asset (when m is non-nil) is
// the manifest itself, reachable the way DownloadAsset actually fetches it:
// by numeric asset ID through the API host, not a browser_download_url.
func manifestForge(t *testing.T, repoName, tag string, m *manifest.Manifest) *github.Client {
	t.Helper()

	mux := http.NewServeMux()
	release := func(w http.ResponseWriter, _ *http.Request) {
		var assets []map[string]any
		if m != nil {
			assets = append(assets, map[string]any{"id": 1, "name": manifest.FileName})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": assets})
	}
	mux.HandleFunc("/repos/"+repoName+"/releases/tags/"+tag, release)
	mux.HandleFunc("/repos/"+repoName+"/releases/assets/1", func(w http.ResponseWriter, _ *http.Request) {
		data, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := github.New("")
	client.SetEndpoints(srv.URL, srv.URL)
	return client
}

// A first release has no previous tag to diff against, so there is nothing
// to fetch: whatShipped must return before it would have made a network
// call, same guarantee TestReleaseNotesSkippedWhenChangelogDisabled proves
// for the changelog-disabled case.
func TestWhatShippedSkipsAFirstRelease(t *testing.T) {
	out := whatShipped(context.Background(), nil, github.Repo{}, "", &manifest.Manifest{Version: "v1.0.0"})
	if out != "" {
		t.Errorf("whatShipped = %q, want empty for a first release", out)
	}
}

// A previous release published before letsgo recorded a manifest leaves
// nothing to fetch; the release must still go out, just without this
// section.
func TestWhatShippedSkipsAPreviousReleaseWithNoManifest(t *testing.T) {
	client := manifestForge(t, "you/demo", "v1.0.0", nil) // no manifest: no asset to find
	repo := github.Repo{Owner: "you", Name: "demo"}
	current := &manifest.Manifest{Version: "v1.1.0", Builder: manifest.Builder{Go: "go1.26.2"}}

	var out string
	stdout := captureStdout(t, func() {
		out = whatShipped(context.Background(), client, repo, "v1.0.0", current)
	})
	if out != "" {
		t.Errorf("whatShipped = %q, want empty", out)
	}
	if !strings.Contains(stdout, `skipped the "what shipped" section`) {
		t.Errorf("stdout = %q, want a skip notice", stdout)
	}
}

func TestWhatShippedRendersTheCollapsedSection(t *testing.T) {
	previous := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.0.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.1"},
	}
	client := manifestForge(t, "you/demo", "v1.0.0", previous)
	repo := github.Repo{Owner: "you", Name: "demo"}
	current := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.1.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.2"},
	}

	out := whatShipped(context.Background(), client, repo, "v1.0.0", current)
	if !strings.Contains(out, "<details><summary>What shipped (vs v1.0.0)</summary>") {
		t.Errorf("whatShipped = %q, want the collapsed summary", out)
	}
	if !strings.Contains(out, "go1.26.1 → go1.26.2") {
		t.Errorf("whatShipped = %q, want the toolchain row", out)
	}
}

// historyFixture writes a repository with two tags, so a local changelog
// Collect can resolve a real "previous" release without touching the
// network — the same value whatShipped's forge fetch below must agree with.
func historyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	identity := []string{"-c", "user.name=Test", "-c", "user.email=t@example.com"}
	run := func(args ...string) {
		t.Helper()
		full := append(append([]string{"-C", dir}, identity...), args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", full, err, out)
		}
	}
	commit := func(name, message string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", message)
	}

	run("init", "-q", "-b", "main")
	commit("a", "feat: first release")
	run("tag", "v1.0.0")
	commit("b", "feat: second release")
	run("tag", "v1.1.0")

	return dir
}

// The notes section must compare against the same previous release the
// changelog above it just used, and must be appended after it (WS-1).
func TestReleaseNotesAppendsWhatShippedUsingTheChangelogsPreviousRelease(t *testing.T) {
	dir := historyFixture(t)

	previous := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.0.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.1"},
	}
	client := manifestForge(t, "you/demo", "v1.0.0", previous)
	repo := github.Repo{Owner: "you", Name: "demo"}

	p := &plan.Plan{
		Features: feature.Resolve(nil),
		Module:   discover.Module{Dir: dir},
		Tag:      "v1.1.0",
	}
	current := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.1.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.2"},
	}

	notes, err := releaseNotes(context.Background(), p, client, repo, current)
	if err != nil {
		t.Fatalf("releaseNotes: %v", err)
	}
	if !strings.Contains(notes, "second release") {
		t.Errorf("notes = %q, want the changelog entry", notes)
	}
	if !strings.Contains(notes, "<details><summary>What shipped (vs v1.0.0)</summary>") {
		t.Errorf("notes = %q, want the what-shipped section against v1.0.0", notes)
	}
	if strings.Index(notes, "second release") > strings.Index(notes, "What shipped") {
		t.Errorf("notes = %q, want the what-shipped section after the changelog", notes)
	}
}

// disable diff-notes must remove the section, and must do so before the
// forge is ever asked for the previous manifest — same no-network-call
// guarantee TestReleaseNotesSkippedWhenChangelogDisabled proves for
// disable changelog. (WS-9)
func TestReleaseNotesOmitsWhatShippedWhenDisabled(t *testing.T) {
	dir := historyFixture(t)

	p := &plan.Plan{
		Features: feature.Resolve([]string{"diff-notes"}),
		Module:   discover.Module{Dir: dir},
		Tag:      "v1.1.0",
	}
	current := &manifest.Manifest{
		Schema: manifest.Schema, Version: "v1.1.0", Builder: manifest.Builder{Tool: "letsgo", Go: "go1.26.2"},
	}

	notes, err := releaseNotes(context.Background(), p, nil, github.Repo{}, current)
	if err != nil {
		t.Fatalf("releaseNotes: %v", err)
	}
	if !strings.Contains(notes, "second release") {
		t.Errorf("notes = %q, want the changelog entry", notes)
	}
	if strings.Contains(notes, "What shipped") {
		t.Errorf("notes = %q, want no what-shipped section", notes)
	}
}

func TestNotesMode(t *testing.T) {
	if notesMode(true, true) != publish.NotesAppend {
		t.Error("--append-notes should append")
	}
	if notesMode(false, true) != publish.NotesReplace {
		t.Error("the default, with changelog enabled, should replace")
	}
	if notesMode(false, false) != publish.NotesAppend {
		t.Error("a disabled changelog should append (nothing), not replace")
	}
}

// Uploads are checked against the digests the release recorded, so every
// published file has to appear here.
func TestSumsFromCoversSourceAndArtifacts(t *testing.T) {
	sums := sumsFrom(&release.Result{
		Source: build.Source{Name: "foo_1.2.3_source.tar.gz", SHA256: "src"},
		Manifest: &manifest.Manifest{Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.3_linux_amd64.tar.gz", SHA256: "aaa"},
			{Name: "foo_1.2.3_darwin_arm64.tar.gz", SHA256: "bbb"},
		}},
	})

	want := map[string]string{
		"foo_1.2.3_source.tar.gz":       "src",
		"foo_1.2.3_linux_amd64.tar.gz":  "aaa",
		"foo_1.2.3_darwin_arm64.tar.gz": "bbb",
	}
	if len(sums) != len(want) {
		t.Fatalf("sums = %v", sums)
	}
	for name, digest := range want {
		if sums[name] != digest {
			t.Errorf("%s = %q, want %q", name, sums[name], digest)
		}
	}
}

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
