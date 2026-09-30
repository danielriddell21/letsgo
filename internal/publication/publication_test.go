package publication

import (
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
	"slices"
	"testing"
)

func TestTag(t *testing.T) {
	if got := Tag(&plan.Plan{Tag: "v1.2.3", Version: "1.2.3"}); got != "v1.2.3" {
		t.Errorf("releaseTag = %q", got)
	}
	// A rehearsal has no tag, and showing an empty one would misrepresent the
	// call being rehearsed.
	if got := Tag(&plan.Plan{Version: "1.2.3-next+abc"}); got != "v1.2.3-next+abc" {
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

func TestSumsFromCoversThePlanFile(t *testing.T) {
	sums := sumsFrom(&release.Result{
		Manifest: &manifest.Manifest{Plan: &manifest.PlanRecord{SHA256: "ppp"}},
	})
	if sums[release.PlanFileName] != "ppp" {
		t.Errorf("sums = %v, want the plan file's digest", sums)
	}
}
func diffFixture(t *testing.T) Options {
	t.Helper()
	p := releasePlan()
	p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}

	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
	result.Manifest = &manifest.Manifest{}

	recorder := publish.NewRecorder(nil)
	return Options{
		Plan: p, Forge: recorder, Tap: recorder, Repo: github.Repo{Owner: "you", Name: "foo"},
		Dir: t.TempDir(), Result: result,
	}
}

func opsOf(actions []plandiff.Action) []plandiff.Op {
	ops := make([]plandiff.Op, len(actions))
	for i, a := range actions {
		ops[i] = a.Op
	}
	return ops
}

func TestObserveAddsTheReleaseAndTheTapFiles(t *testing.T) {
	actions, err := Observe(t.Context(), diffFixture(t))
	if err != nil {
		t.Fatal(err)
	}

	// The release, then the formula and its @next.
	want := []plandiff.Op{plandiff.Add, plandiff.Add, plandiff.Add}
	if got := opsOf(actions); !slices.Equal(got, want) {
		t.Errorf("ops = %v, want %v\n%+v", got, want, actions)
	}
}

func TestObserveLeavesTheTapAloneForADraft(t *testing.T) {
	targets := diffFixture(t)
	targets.Plan.Config.Draft = true

	actions, err := Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Kind != plandiff.KindRelease {
		t.Errorf("actions = %+v, want the release only", actions)
	}
}
