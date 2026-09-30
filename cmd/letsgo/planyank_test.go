package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// yankForge is a forge holding one release, which it lets be edited.
type yankForge struct {
	mu      sync.Mutex
	release github.Release
	patches int
}

// yankForgeFor serves the release v1.2.3 of you/demo and points the commands
// at it.
func yankForgeFor(t *testing.T) *yankForge {
	t.Helper()
	forge := &yankForge{release: github.Release{ID: 7, TagName: "v1.2.3", Body: "### Features\n\n- a thing\n"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forge.mu.Lock()
		defer forge.mu.Unlock()

		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases/tags/v1.2.3"):
			_ = json.NewEncoder(w).Encode(forge.release)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tags"):
			_, _ = w.Write([]byte(`[{"name":"v1.2.3"},{"name":"v1.2.2"}]`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/releases/7"):
			forge.patches++
			var in github.ReleaseInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			forge.release.Body, forge.release.Prerelease = in.Body, in.Prerelease
			_ = json.NewEncoder(w).Encode(forge.release)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)

	previous := newForgeClient
	t.Cleanup(func() { newForgeClient = previous })
	newForgeClient = func(token string) *github.Client {
		client := github.New(token)
		client.SetEndpoints(srv.URL, srv.URL)
		return client
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	return forge
}

// A yank is planned, saved, and applied, and the apply does what the plan
// listed and nothing else.
func TestAYankPlanIsAppliedExactlyAsPlanned(t *testing.T) {
	forge := yankForgeFor(t)
	dir := moduleFixture(t)
	t.Chdir(dir)
	path := filepath.Join(t.TempDir(), "y.plan")

	var err error
	out := captureStdout(t, func() {
		err = runPlan([]string{"-yank", "v1.2.3", "-reason", "bad build", "-out", path, "--exit-code"})
	})
	if !errors.Is(err, errPlanChanges) {
		t.Fatalf("plan -yank = %v, want the plan to have changes\n%s", err, out)
	}
	for _, want := range []string{"~ release", "~ gomod", "v1.2.3", "go.mod", "saved to " + path} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if forge.patches != 0 {
		t.Fatalf("planning edited the release %d time(s)", forge.patches)
	}
	if got := readText(t, filepath.Join(dir, "go.mod")); strings.Contains(got, "retract") {
		t.Fatalf("planning edited go.mod:\n%s", got)
	}

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != plandiff.FileKindYank || file.Tag != "v1.2.3" || file.Reason != "bad build" || file.Previous != "v1.2.2" {
		t.Errorf("saved %+v", file)
	}

	out = captureStdout(t, func() { err = runApply([]string{path}) })
	if err != nil {
		t.Fatalf("apply = %v\n%s", err, out)
	}
	if forge.patches != 1 || !forge.release.Prerelease || !yank.IsRetracted(forge.release.Body) {
		t.Errorf("patches = %d, release = %+v", forge.patches, forge.release)
	}
	if got := readText(t, filepath.Join(dir, "go.mod")); !strings.Contains(got, "v1.2.3 // bad build") {
		t.Errorf("go.mod:\n%s", got)
	}
	if !strings.Contains(out, "v1.2.3 is retracted") {
		t.Errorf("apply did not say what comes next:\n%s", out)
	}

	// Applying it again finds everything as planned, and writes nothing.
	out = captureStdout(t, func() { err = runApply([]string{path}) })
	if err != nil {
		t.Fatalf("second apply = %v\n%s", err, out)
	}
	if forge.patches != 1 || !strings.Contains(out, "already as planned") {
		t.Errorf("second apply: patches = %d\n%s", forge.patches, out)
	}
}

// A go.mod that changed after the plan was made is not the go.mod the plan
// agreed, so nothing is written.
func TestAYankPlanIsRefusedOnceGoModHasChanged(t *testing.T) {
	forge := yankForgeFor(t)
	dir := moduleFixture(t)
	t.Chdir(dir)
	path := filepath.Join(t.TempDir(), "y.plan")

	_ = captureStdout(t, func() { _ = runPlan([]string{"-yank", "v1.2.3", "-out", path}) })
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var err error
	_ = captureStdout(t, func() { err = runApply([]string{path}) })
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "plan -yank v1.2.3 -out") {
		t.Fatalf("apply = %v, want a stale plan", err)
	}
	if forge.patches != 0 {
		t.Errorf("a stale plan edited the release")
	}
}

func TestAYankPlanIsRefusedForAnotherRepository(t *testing.T) {
	yankForgeFor(t)
	t.Chdir(moduleFixture(t))
	path := filepath.Join(t.TempDir(), "y.plan")
	_ = captureStdout(t, func() { _ = runPlan([]string{"-yank", "v1.2.3", "-out", path}) })

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Repo = "someone/else"
	if _, err := writePlan(file, path); err != nil {
		t.Fatal(err)
	}

	_ = captureStdout(t, func() { err = runApply([]string{path}) })
	if err == nil || !strings.Contains(err.Error(), "someone/else") {
		t.Errorf("apply = %v, want the repository named", err)
	}
}

func TestPlanYankHasNoJSONForm(t *testing.T) {
	if err := runPlan([]string{"-yank", "v1.2.3", "-json"}); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanYankNamesAReleaseThatDoesNotExist(t *testing.T) {
	yankForgeFor(t)
	t.Chdir(moduleFixture(t))
	var err error
	_ = captureStdout(t, func() { err = runPlan([]string{"-yank", "v9.9.9"}) })
	if err == nil {
		t.Error("planned the retraction of a release that does not exist")
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// What a yank would do is what the real yank does, seen without doing it.
func TestObserveYankSeesEachWriteAndMakesNone(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}

	actions, err := observeYank(context.Background(), yank.Options{
		Client: recorder, Repo: github.Repo{Owner: "you", Name: "demo"}, Tag: "v1.2.3", Reason: "bad", GoMod: goMod,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := opsOf(actions), []plandiff.Op{plandiff.Change, plandiff.Change}; !slices.Equal(got, want) {
		t.Fatalf("ops = %v, want %v\n%+v", got, want, actions)
	}
	if actions[1].Kind != plandiff.KindGoMod || actions[1].Target != "go.mod" || actions[1].Observed == actions[1].Planned {
		t.Errorf("go.mod action = %+v", actions[1])
	}
	if got := readText(t, goMod); strings.Contains(got, "retract") {
		t.Errorf("observing edited go.mod:\n%s", got)
	}
}

func TestObserveYankKeepsWhatIsAlreadyRetracted(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/demo\n\nretract v1.2.3 // bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Prerelease: true, Body: "> [!CAUTION]\n> retracted\n\nnotes"}

	actions, err := observeYank(context.Background(), yank.Options{
		Client: recorder, Repo: github.Repo{Owner: "you", Name: "demo"}, Tag: "v1.2.3", Reason: "bad", GoMod: goMod,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := opsOf(actions), []plandiff.Op{plandiff.Keep, plandiff.Keep}; !slices.Equal(got, want) {
		t.Errorf("ops = %v, want %v", got, want)
	}
}

func TestObserveYankNamesAGoModInAScope(t *testing.T) {
	goMod := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/demo/api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "api/v1.2.3", Body: "notes"}

	actions, err := observeYank(context.Background(), yank.Options{
		Client: recorder, Repo: github.Repo{Owner: "you", Name: "demo"}, Tag: "api/v1.2.3", Prefix: "api/", GoMod: goMod,
	})
	if err != nil {
		t.Fatal(err)
	}
	if actions[1].Target != "api/go.mod" {
		t.Errorf("target = %q, want api/go.mod", actions[1].Target)
	}
}

func TestObserveYankReportsAGoModThatIsNotThere(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}
	_, err := observeYank(context.Background(), yank.Options{
		Client: recorder, Repo: github.Repo{Owner: "you", Name: "demo"}, Tag: "v1.2.3",
		GoMod: filepath.Join(t.TempDir(), "go.mod"),
	})
	if err == nil {
		t.Error("observed a retraction of a module with no go.mod")
	}
}

func TestGoModObserverRefusesAWriteWithoutARead(t *testing.T) {
	if err := (&goModObserver{}).write("go.mod", []byte("x")); err == nil {
		t.Error("a write with no read was accepted")
	}
}

func TestYankStalenessHoldsTheWritesToThePlan(t *testing.T) {
	saved := []plandiff.Action{
		{Op: plandiff.Change, Kind: plandiff.KindGoMod, Target: "go.mod", Observed: "blob:a", Planned: "blob:b"},
		{Op: plandiff.Keep, Kind: plandiff.KindTap, Target: "Formula/x.rb", Observed: "blob:c", Planned: "blob:c"},
	}
	remake := "letsgo plan -yank v1 -out"

	for name, tc := range map[string]struct {
		current []plandiff.Action
		want    string
	}{
		"as planned": {saved, ""},
		"already done": {[]plandiff.Action{
			{Op: plandiff.Keep, Kind: plandiff.KindGoMod, Target: "go.mod", Observed: "blob:b", Planned: "blob:b"},
		}, ""},
		"moved": {[]plandiff.Action{
			{Op: plandiff.Change, Kind: plandiff.KindGoMod, Target: "go.mod", Observed: "blob:z", Planned: "blob:y"},
		}, "stale"},
		"would write something else": {[]plandiff.Action{
			{Op: plandiff.Change, Kind: plandiff.KindGoMod, Target: "go.mod", Observed: "blob:a", Planned: "blob:y"},
		}, "would now write"},
	} {
		t.Run(name, func(t *testing.T) {
			err := yankStaleness(saved, tc.current, remake)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("err = %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), remake)):
				t.Errorf("err = %v, want %q and the remake hint", err, tc.want)
			}
		})
	}
}

func TestGuardYankRefusesWhatThePlanDidNotList(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3"}
	repo := github.Repo{Owner: "you", Name: "demo"}
	nothing := newPlannedWrites(nil)

	guarded := guardYank(yank.Options{Client: recorder, TapAPI: recorder, Tag: "v1.2.3"}, nothing)
	if _, err := guarded.Client.UpdateRelease(context.Background(), repo, 7, github.ReleaseInput{}); err == nil {
		t.Error("the release was edited though the plan did not list it")
	}
	if err := guarded.TapAPI.WriteFile(context.Background(), repo, github.FileInput{Path: "Formula/x.rb"}); err == nil {
		t.Error("the tap was written though the plan did not list it")
	}
	if err := guarded.WriteGoMod(filepath.Join(t.TempDir(), "go.mod"), []byte("x")); err == nil {
		t.Error("go.mod was written though the plan did not list it")
	}
	if len(recorder.Calls) != 0 {
		t.Errorf("a refused write reached the forge: %v", recorder.Calls)
	}
}

func TestGuardYankAllowsWhatThePlanListed(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3"}
	repo := github.Repo{Owner: "you", Name: "demo"}
	goMod := filepath.Join(t.TempDir(), "go.mod")
	listed := newPlannedWrites([]plandiff.Action{
		{Op: plandiff.Change, Kind: plandiff.KindRelease, Target: "v1.2.3"},
		{Op: plandiff.Change, Kind: plandiff.KindGoMod, Target: "go.mod"},
		{Op: plandiff.Add, Kind: plandiff.KindTap, Target: "Formula/x.rb"},
	})

	guarded := guardYank(yank.Options{Client: recorder, TapAPI: recorder, Tag: "v1.2.3"}, listed)
	if _, err := guarded.Client.UpdateRelease(context.Background(), repo, 7, github.ReleaseInput{}); err != nil {
		t.Error(err)
	}
	if err := guarded.TapAPI.WriteFile(context.Background(), repo, github.FileInput{Path: "Formula/x.rb"}); err != nil {
		t.Error(err)
	}
	if err := guarded.WriteGoMod(goMod, []byte("module x\n")); err != nil {
		t.Error(err)
	}
	if got := readText(t, goMod); got != "module x\n" {
		t.Errorf("go.mod = %q", got)
	}
}

// The formula and @next roll back with the release, and a formula already at
// the previous release is left as it is.
func TestObserveYankPlansTheTapRollback(t *testing.T) {
	repo := github.Repo{Owner: "you", Name: "demo"}
	artifact := manifest.Artifact{Name: "demo_1.2.2_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64", Binary: "demo", SHA256: "aaa"}
	at := func(version string) *manifest.Manifest {
		return &manifest.Manifest{Version: version, Tag: "v" + version, Artifacts: []manifest.Artifact{artifact}}
	}
	render := func(version string, next bool) []byte {
		f := yank.FormulasFrom(at(version), repo, "demo", "")[0]
		if next {
			f.Name = brew.NextName(f.Name)
		}
		content, err := f.Render()
		if err != nil {
			t.Fatal(err)
		}
		return content
	}

	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}
	recorder.Files = map[string][]byte{
		"Formula/demo.rb":      render("1.2.3", false),
		"Formula/demo@next.rb": render("1.2.3", true),
	}

	actions, err := observeYank(context.Background(), yank.Options{
		Client: recorder, Repo: repo, Tag: "v1.2.3", Reason: "bad", Project: "demo", Previous: "v1.2.2",
		Tap: github.Repo{Owner: "you", Name: "homebrew-tap"}, TapAPI: recorder,
		Manifests: func(context.Context, string) (*manifest.Manifest, error) { return at("1.2.2"), nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]plandiff.Op{}
	for _, a := range actions {
		if a.Kind == plandiff.KindTap {
			got[a.Target] = a.Op
		}
	}
	want := map[string]plandiff.Op{"Formula/demo.rb": plandiff.Change, "Formula/demo@next.rb": plandiff.Change}
	if len(got) != len(want) || got["Formula/demo.rb"] != plandiff.Change || got["Formula/demo@next.rb"] != plandiff.Change {
		t.Errorf("tap actions = %v, want %v", got, want)
	}
	for _, call := range recorder.Calls {
		if strings.HasPrefix(call, "PUT") || strings.HasPrefix(call, "PATCH") {
			t.Errorf("observing wrote: %s", call)
		}
	}
}

func TestPlanYankFormatMarkdown(t *testing.T) {
	yankForgeFor(t)
	t.Chdir(moduleFixture(t))

	var err error
	out := captureStdout(t, func() { err = runPlan([]string{"-yank", "v1.2.3", "--format", "md"}) })
	if err != nil {
		t.Fatalf("plan -yank --format md = %v\n%s", err, out)
	}
	for _, want := range []string{"## letsgo plan: yank v1.2.3\n", "```diff\n! release", "\n! gomod"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}
