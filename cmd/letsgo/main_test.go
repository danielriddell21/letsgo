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
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
)

// moduleFixture writes a minimal buildable module and commits it, so
// plan.Resolve has a real repository to work from.
func moduleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/demo\n\ngo 1.24\n")
	write("main.go", `package main

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
		return
	}
}
`)
	write("letsgo.mod", "build "+gobuild.Host().String()+"\n")

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("remote", "add", "origin", "https://github.com/you/demo.git")
	run("add", ".")
	run("commit", "-q", "-m", "feat: first release")
	run("tag", "v1.2.3")
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
