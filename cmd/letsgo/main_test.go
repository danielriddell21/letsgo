package main

import (
	"context"
	"flag"
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

// A worktree always checks out the whole repository, so a nested module has
// to be compared at <worktree>/relDir, never at the worktree's own root: the
// same bug `plan.checkoutTag` had, in the command that proposes a tag rather
// than the one that resolves one.
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

// disable changelog must stop the changelog from being built at all, not
// merely from being shown: a nil client proves this returns before it would
// have made a network call.
func TestReleaseNotesSkippedWhenChangelogDisabled(t *testing.T) {
	p := &plan.Plan{Features: feature.Resolve([]string{"changelog"})}

	notes, err := releaseNotes(context.Background(), p, nil, github.Repo{})
	if err != nil || notes != "" {
		t.Errorf("releaseNotes = (%q, %v), want empty and no error", notes, err)
	}
}

func TestNotesMode(t *testing.T) {
	if notesMode(true) != publish.NotesAppend {
		t.Error("--append-notes should append")
	}
	if notesMode(false) != publish.NotesReplace {
		t.Error("the default should replace")
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
