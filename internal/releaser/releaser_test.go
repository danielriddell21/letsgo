package releaser_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/discover"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/internal/release"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/forgetest"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/releaser"
	"github.com/danielriddell21/letsgo/manifest"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

const token = "test-token"

// rig is a module on disk and a fake forge, wired the way a command wires a
// release: one client for the forge, injected everywhere.
type rig struct {
	forge  *forgetest.Fake
	client *github.Client
	module string
	log    *bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fake := forgetest.NewFake(t, "you/demo")

	client := github.New(token)
	client.SetEndpoints(fake.URL(), fake.URL())

	return &rig{forge: fake, client: client, module: forgetest.Module(t), log: &bytes.Buffer{}}
}

// options is a release of the rig's module through its forge. No machine
// config is read: Global is given empty, so a test never depends on the
// machine it runs on, and nothing changes the working directory.
func (r *rig) options(t *testing.T) releaser.Options {
	t.Helper()
	return releaser.Options{
		Plan: plan.Options{
			Dir: r.module, Publish: true, Credentials: credential.Set{Forge: credential.Credential{Value: token, Source: "--token"}},
			Global:  &config.Global{},
			Analyse: false, DisableProxyWarm: true,
			NewClient: func(string) *github.Client { return r.client },
		},
		Clients:     releaser.Clients{Read: r.client, Release: r.client, Tap: r.client},
		Token:       token,
		ToolVersion: "test",
		Out:         filepath.Join(t.TempDir(), "dist"),
		Log:         r.log,
	}
}

// A release goes through the injected clients from the first gate to the last
// upload: the plan checks write access, the release is created, every built
// file is attached, and nothing reaches an endpoint the fake does not serve.
func TestReleasePublishesThroughTheInjectedClients(t *testing.T) {
	r := newRig(t)

	res, err := releaser.Release(context.Background(), r.options(t))
	if err != nil {
		t.Fatalf("Release = %v\n%s", err, r.log)
	}

	if missed := r.forge.Unhandled(); len(missed) > 0 {
		t.Errorf("requests the forge does not serve: %v", missed)
	}
	rel := r.forge.Release("v1.2.3")
	if rel == nil {
		t.Fatalf("no release was published\n%s", r.log)
	}
	if rel.Draft || rel.Prerelease {
		t.Errorf("release = draft %v, prerelease %v; want a stable release", rel.Draft, rel.Prerelease)
	}
	if body, ok := r.forge.Asset("v1.2.3", manifest.FileName); !ok || len(body) == 0 {
		t.Errorf("the manifest was not uploaded; assets = %v", rel.Assets)
	}
	if res.URL != rel.HTMLURL || res.Snapshot || res.Dir == "" || res.Build == nil || res.Took <= 0 {
		t.Errorf("result = %+v, want the release's URL, a build, and no rehearsal", res)
	}
	if !strings.Contains(r.log.String(), "built ") {
		t.Errorf("progress does not report the build:\n%s", r.log)
	}
}

// ADR-0022 cites the --draft bug that shipped because nothing tested this
// sequence: a draft release must be created as a draft, not published.
func TestReleaseWithDraftCreatesADraft(t *testing.T) {
	r := newRig(t)
	o := r.options(t)
	o.Draft = true

	if _, err := releaser.Release(context.Background(), o); err != nil {
		t.Fatalf("Release = %v\n%s", err, r.log)
	}
	rel := r.forge.Release("v1.2.3")
	if rel == nil {
		t.Fatalf("no release was created\n%s", r.log)
	}
	if !rel.Draft {
		t.Error("--draft produced a published release")
	}
}

// A rehearsal reaches every decision a real run reaches and writes nothing:
// the recorder says what would have been called.
func TestReleaseSnapshotRehearsesWithoutWriting(t *testing.T) {
	r := newRig(t)
	o := r.options(t)
	o.Snapshot = true
	o.Plan.Snapshot, o.Plan.Publish = true, false

	res, err := releaser.Release(context.Background(), o)
	if err != nil {
		t.Fatalf("Release = %v\n%s", err, r.log)
	}
	if r.forge.Release("v1.2.3") != nil {
		t.Error("a rehearsal published a release")
	}
	if !res.Snapshot || res.URL != "" {
		t.Errorf("result = %+v, want a rehearsal with no URL", res)
	}
	if out := r.log.String(); !strings.Contains(out, "rehearsal: the calls below would be made, and are not") {
		t.Errorf("progress does not say it is a rehearsal:\n%s", out)
	}
}

// A plan that fails its gates stops everything: the failure is reported once,
// by the plan output, and nothing is built or published.
func TestReleaseStopsWhenThePlanFails(t *testing.T) {
	r := newRig(t)
	// An untracked file makes the worktree dirty, which a release refuses.
	if err := os.WriteFile(filepath.Join(r.module, "dirty.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := releaser.Release(context.Background(), r.options(t))
	if !errors.Is(err, releaser.ErrPlanFailed) {
		t.Fatalf("err = %v, want ErrPlanFailed\n%s", err, r.log)
	}
	if r.forge.Release("v1.2.3") != nil {
		t.Error("a release was published from a failed plan")
	}
	if out := r.log.String(); !strings.Contains(out, "plan failed in") || !strings.Contains(out, "nothing was built or published") {
		t.Errorf("progress does not report the failure:\n%s", out)
	}
}

func TestBuildProducesFilesAndTouchesNoForge(t *testing.T) {
	r := newRig(t)
	o := r.options(t)
	o.Clients = releaser.Clients{} // `letsgo build` has no forge at all
	o.Plan.Publish = false

	res, err := releaser.Build(context.Background(), o)
	if err != nil {
		t.Fatalf("Build = %v\n%s", err, r.log)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, manifest.FileName)); err != nil {
		t.Errorf("the manifest was not built: %v", err)
	}
	if len(res.Build.Files) == 0 || res.URL != "" {
		t.Errorf("result = %+v, want built files and no URL", res)
	}
	if missed := r.forge.Unhandled(); len(missed) > 0 {
		t.Errorf("a build touched the forge: %v", missed)
	}
}

func TestBuildStopsWhenThePlanFails(t *testing.T) {
	r := newRig(t)
	if err := os.WriteFile(filepath.Join(r.module, "dirty.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := r.options(t)
	o.Clients, o.Plan.Publish = releaser.Clients{}, false

	if _, err := releaser.Build(context.Background(), o); !errors.Is(err, releaser.ErrPlanFailed) {
		t.Fatalf("err = %v, want ErrPlanFailed", err)
	}
	if !strings.Contains(r.log.String(), "nothing was built\n") {
		t.Errorf("a build's failure should say nothing was built:\n%s", r.log)
	}
}

func TestReleaseAndDiffNeedAllTheirClients(t *testing.T) {
	r := newRig(t)
	o := r.options(t)
	o.Clients = releaser.Clients{Read: r.client}

	if _, err := releaser.Release(context.Background(), o); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("Release err = %v, want a missing-client error", err)
	}
	if _, err := releaser.Diff(context.Background(), &plan.Plan{Source: plan.Source{Location: discover.Location{Repo: discover.Repo{Owner: "you", Name: "demo"}}}}, o); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("Diff err = %v, want a missing-client error", err)
	}
}

// The saved-plan path end to end: diff against an empty forge, save the plan,
// read it back, and apply it. What was planned is what is published, and the
// plan itself is recorded in the release.
func TestAPlanSavedFromDiffCanBeAppliedExactly(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	o := r.options(t)

	p, err := plan.Resolve(ctx, o.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !p.OK() {
		var out bytes.Buffer
		p.Report(&out, false)
		t.Fatalf("plan is not OK:\n%s", out.String())
	}

	d, err := releaser.Diff(ctx, p, o)
	if err != nil {
		t.Fatalf("Diff = %v\n%s", err, r.log)
	}
	if !plandiff.HasChanges(d.Actions) {
		t.Fatal("a release into an empty forge should have changes")
	}

	path := filepath.Join(t.TempDir(), "release.plan")
	if _, err := apply.Save(p, d, path, "test"); err != nil {
		t.Fatal(err)
	}
	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	o.Applied = file
	res, err := releaser.Release(ctx, o)
	if err != nil {
		t.Fatalf("Release(applied) = %v\n%s", err, r.log)
	}
	if rel := r.forge.Release("v1.2.3"); rel == nil || res.URL != rel.HTMLURL {
		t.Fatalf("the planned release was not published: %v\n%s", rel, r.log)
	}
	if _, ok := r.forge.Asset("v1.2.3", release.PlanFileName); !ok {
		t.Error("the plan was not recorded in the release")
	}
	if !strings.Contains(r.log.String(), "the rebuild matches the plan") {
		t.Errorf("the rebuild was not held to the plan:\n%s", r.log)
	}
}

// A saved plan is a promise about what will happen: when the forge has moved
// on since, nothing is published.
func TestAStalePlanPublishesNothing(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	o := r.options(t)

	p, err := plan.Resolve(ctx, o.Plan)
	if err != nil {
		t.Fatal(err)
	}
	d, err := releaser.Diff(ctx, p, o)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.plan")
	if _, err := apply.Save(p, d, path, "test"); err != nil {
		t.Fatal(err)
	}
	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	// Someone else publishes the release after the plan was made.
	if _, err := r.client.CreateRelease(ctx, github.Repo{Owner: "you", Name: "demo"}, github.ReleaseInput{TagName: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}

	o.Applied = file
	if _, err := releaser.Release(ctx, o); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("err = %v, want a stale-plan error\n%s", err, r.log)
	}
	if rel := r.forge.Release("v1.2.3"); rel != nil && len(rel.Assets) > 0 {
		t.Errorf("a stale plan uploaded assets: %v", rel.Assets)
	}
}

func TestTookKeepsSubSecondDetail(t *testing.T) {
	if got := releaser.Took(time.Now().Add(-40 * time.Millisecond)); got == 0 {
		t.Errorf("Took = %v, want a measurable duration", got)
	}
	if got := releaser.Took(time.Now().Add(-90 * time.Second)); got.Round(time.Second) != 90*time.Second {
		t.Errorf("Took = %v", got)
	}
}

// A draft the config asks for survives an absent --draft: the flag can only
// add to what letsgo.mod says, never clear it.
func TestReleaseKeepsADraftTheConfigAsksFor(t *testing.T) {
	r := newRig(t)
	r.module = forgetest.ModuleWith(t, "release draft=true\n", nil)

	if _, err := releaser.Release(context.Background(), r.options(t)); err != nil {
		t.Fatalf("Release = %v\n%s", err, r.log)
	}
	if rel := r.forge.Release("v1.2.3"); rel == nil || !rel.Draft {
		t.Errorf("release = %+v, want a draft: letsgo.mod says draft=true", rel)
	}
}

// A draft holds back the tap, which would otherwise point the world at assets
// only the author can see; without one the rehearsal reaches the tap.
func TestReleaseDraftHoldsBackTheTap(t *testing.T) {
	for _, tc := range []struct {
		name        string
		draft       bool
		wantSkipped bool
	}{
		{"with a draft", true, true},
		{"without a draft", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.module = forgetest.ModuleWith(t, tapConfig, nil)
			o := r.options(t)
			o.Snapshot, o.Draft = true, tc.draft
			o.Plan.Snapshot, o.Plan.Publish = true, false

			if _, err := releaser.Release(context.Background(), o); err != nil {
				t.Fatalf("Release = %v\n%s", err, r.log)
			}
			if got := strings.Contains(r.log.String(), "skipped the Homebrew tap"); got != tc.wantSkipped {
				t.Errorf("tap skipped = %v, want %v\n%s", got, tc.wantSkipped, r.log)
			}
		})
	}
}

// The repository's description is read when there is a tap to write a formula
// into, and only then: a release with none never touches the endpoint, and a
// build has no forge to read.
func TestBuildReadsTheRepositoryOnlyForATap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		read   bool
		want   bool
	}{
		{"a tap and a forge", tapConfig, true, true},
		{"no tap", "", true, false},
		{"a tap but no forge to read", tapConfig, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.module = forgetest.ModuleWith(t, tc.config, nil)
			o := r.options(t)
			o.Plan.Publish = false
			if !tc.read {
				o.Clients = releaser.Clients{}
			}

			res, err := releaser.Build(context.Background(), o)
			if err != nil {
				t.Fatalf("Build = %v\n%s", err, r.log)
			}
			if got := res.Info != nil; got != tc.want {
				t.Fatalf("Info = %+v, want read = %v", res.Info, tc.want)
			}
			if tc.want && (res.Info.Description != "a demo" || res.Info.License != "MIT") {
				t.Errorf("Info = %+v, want what the forge said", res.Info)
			}
		})
	}
}

func TestPlanReportsAndOptionallyDiffs(t *testing.T) {
	ctx := context.Background()

	t.Run("a plan alone builds and reads nothing", func(t *testing.T) {
		r := newRig(t)
		planned, err := releaser.Plan(ctx, releaser.PlanOptions{Options: r.options(t), Explain: true})
		if err != nil {
			t.Fatalf("Plan = %v\n%s", err, r.log)
		}
		if planned.Plan == nil || planned.Diff != nil || planned.Started.IsZero() {
			t.Errorf("planned = %+v, want a plan, no diff and a start time", planned)
		}
		if missed := r.forge.Unhandled(); len(missed) > 0 {
			t.Errorf("a plan touched endpoints the fake does not serve: %v", missed)
		}
		if r.log.Len() == 0 {
			t.Error("the plan was not reported")
		}
	})

	t.Run("with a diff, it says what releasing would change", func(t *testing.T) {
		r := newRig(t)
		planned, err := releaser.Plan(ctx, releaser.PlanOptions{Options: r.options(t), Diff: true})
		if err != nil {
			t.Fatalf("Plan = %v\n%s", err, r.log)
		}
		if planned.Diff == nil || !plandiff.HasChanges(planned.Diff.Actions) {
			t.Errorf("diff = %+v, want changes against an empty forge", planned.Diff)
		}
		if r.forge.Release("v1.2.3") != nil {
			t.Error("a diff created a release")
		}
	})

	t.Run("a failing plan says so once", func(t *testing.T) {
		r := newRig(t)
		if err := os.WriteFile(filepath.Join(r.module, "dirty.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := releaser.Plan(ctx, releaser.PlanOptions{Options: r.options(t)})
		if !errors.Is(err, releaser.ErrPlanFailed) {
			t.Fatalf("err = %v, want ErrPlanFailed", err)
		}
		if got := strings.Count(r.log.String(), "plan failed in"); got != 1 {
			t.Errorf("failure reported %d times:\n%s", got, r.log)
		}
	})
}

// tapConfig asks for a tap, and for a Linux build alongside the host's: a
// formula needs a macOS or Linux build to install, which a Windows host does
// not otherwise produce. linux/arm64 is not any CI host, so it never repeats
// the host's own target.
const tapConfig = "build linux/arm64\nbrew you/homebrew-tap\n"
