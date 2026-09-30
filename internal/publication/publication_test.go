package publication

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
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

// fakeForge is a forge holding no releases that hands out one on request.
type fakeForge struct {
	created []github.ReleaseInput
	err     error
}

func (f *fakeForge) ReleaseByTag(context.Context, github.Repo, string) (*github.Release, error) {
	return nil, nil
}

func (f *fakeForge) CreateRelease(_ context.Context, _ github.Repo, in github.ReleaseInput) (*github.Release, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.created = append(f.created, in)
	return &github.Release{ID: 7, TagName: in.TagName}, nil
}

func (f *fakeForge) UpdateRelease(context.Context, github.Repo, int64, github.ReleaseInput) (*github.Release, error) {
	return nil, nil
}

func (f *fakeForge) Assets(context.Context, github.Repo, int64) ([]github.Asset, error) {
	return nil, nil
}

func (f *fakeForge) DeleteAsset(context.Context, github.Repo, int64) error { return nil }

func (f *fakeForge) UploadAsset(context.Context, github.Repo, int64, string, int64, io.Reader) (*github.Asset, error) {
	return &github.Asset{}, nil
}

func publishFixture(t *testing.T) (Options, *fakeForge, *fakeTap) {
	t.Helper()
	forge, tap := &fakeForge{}, &fakeTap{}
	o := diffFixture(t)
	o.Forge, o.Tap = forge, tap
	o.Snapshot = true
	return o, forge, tap
}

func TestPublishCreatesTheReleaseThenWritesTheTap(t *testing.T) {
	o, forge, tap := publishFixture(t)
	var out strings.Builder
	o.Out = &out

	result, err := Publish(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if result.Forge == nil || !result.Forge.Created {
		t.Errorf("Forge = %+v, want a created release", result.Forge)
	}
	if len(forge.created) != 1 || forge.created[0].TagName != "v1.2.3" {
		t.Errorf("created = %+v, want one release for v1.2.3", forge.created)
	}
	if want := []string{"Formula/foo.rb", "Formula/foo@next.rb"}; !slices.Equal(tap.writes, want) {
		t.Errorf("tap writes = %v, want %v", tap.writes, want)
	}

	// The tap names assets that exist only once the release is attached.
	report, formula := strings.Index(out.String(), "uploaded 0, skipped 0"), strings.Index(out.String(), "Formula/foo.rb")
	if report < 0 || formula < report {
		t.Errorf("the release was not reported before the tap:\n%s", out.String())
	}
}

func TestPublishStopsBeforeTheTapWhenTheForgeFails(t *testing.T) {
	o, forge, tap := publishFixture(t)
	forge.err = errors.New("forbidden")

	if _, err := Publish(t.Context(), o); err == nil {
		t.Fatal("a failed release went on to publish")
	}
	if len(tap.writes) != 0 {
		t.Errorf("tap writes = %v after a failed release, want none", tap.writes)
	}
}

func TestPublishHoldsBackTheTapForADraft(t *testing.T) {
	o, forge, tap := publishFixture(t)
	o.Plan.Config.Draft = true

	if _, err := Publish(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if len(forge.created) != 1 || !forge.created[0].Draft {
		t.Errorf("created = %+v, want one draft", forge.created)
	}
	if len(tap.writes) != 0 {
		t.Errorf("a draft wrote to the tap: %v", tap.writes)
	}
}

func TestReportPublished(t *testing.T) {
	var out strings.Builder
	reportPublished(&out, &publish.Result{
		Uploaded: []string{"a"}, Skipped: []string{"b", "c"}, Replaced: []string{"d"}, NotesRefused: true,
	})

	for _, want := range []string{"description could not be updated", "uploaded 1, skipped 2, replaced 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestDownstreamStopsWhenTheTapFails(t *testing.T) {
	o, _, _ := publishFixture(t)
	o.Tap = failingTap{}

	if err := Downstream(t.Context(), o); err == nil {
		t.Error("a tap that could not be read did not stop the publication")
	}
}

func TestDownstreamPushesNothingForAnEmptyTapAndImageSet(t *testing.T) {
	o, _, tap := publishFixture(t)
	o.Plan.Tap = github.Repo{}

	if err := Downstream(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if len(tap.writes) != 0 {
		t.Errorf("tap writes = %v with no tap configured", tap.writes)
	}
}

func TestOptionsOutDiscardsWhenNil(t *testing.T) {
	if got := (Options{}).out(); got != io.Discard {
		t.Errorf("out() = %v, want io.Discard", got)
	}
	var b strings.Builder
	if got := (Options{Out: &b}).out(); got != &b {
		t.Error("out() did not return the writer it was given")
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
