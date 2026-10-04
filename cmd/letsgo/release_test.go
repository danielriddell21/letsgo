package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/manifest"
)

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

	if missed := ff.Unhandled(); len(missed) > 0 {
		t.Errorf("requests the forge does not serve: %v", missed)
	}
	rel := ff.Release("v1.2.3")
	if rel == nil {
		t.Fatalf("no release was published\n%s", out)
	}
	if rel.Draft || rel.Prerelease {
		t.Errorf("release = draft %v, prerelease %v; want a stable release", rel.Draft, rel.Prerelease)
	}
	if len(rel.Assets) == 0 {
		t.Fatalf("release has no assets\n%s", out)
	}
	if body, ok := ff.Asset("v1.2.3", manifest.FileName); !ok || len(body) == 0 {
		t.Errorf("the manifest was not uploaded; assets = %v", rel.Assets)
	}
	if !strings.Contains(out, "released in") || !strings.Contains(out, rel.HTMLURL) {
		t.Errorf("output does not report the release:\n%s", out)
	}
}
