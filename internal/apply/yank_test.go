package apply

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	"github.com/danielriddell21/letsgo/manifest"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// What a yank would do is what the real yank does, seen without doing it.
func TestObserveYankSeesEachWriteAndMakesNone(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}

	actions, err := ObserveYank(context.Background(), yank.Options{
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

	actions, err := ObserveYank(context.Background(), yank.Options{
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

	actions, err := ObserveYank(context.Background(), yank.Options{
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
	_, err := ObserveYank(context.Background(), yank.Options{
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

func TestYankStaleHoldsTheWritesToThePlan(t *testing.T) {
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
			err := YankStale(saved, tc.current, remake)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("err = %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), remake)):
				t.Errorf("err = %v, want %q and the remake hint", err, tc.want)
			}
		})
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
		f := brew.FormulasFor(at(version), repo, nil, "demo", "")[0]
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

	actions, err := ObserveYank(context.Background(), yank.Options{
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

func opsOf(actions []plandiff.Action) []plandiff.Op {
	ops := make([]plandiff.Op, len(actions))
	for i, a := range actions {
		ops[i] = a.Op
	}
	return ops
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func yankOptionsFor(t *testing.T, recorder *publish.Recorder) (yank.Options, string) {
	t.Helper()
	goMod := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return yank.Options{
		Client: recorder, Repo: github.Repo{Owner: "you", Name: "demo"}, Tag: "v1.2.3", Reason: "bad", GoMod: goMod,
	}, goMod
}

func TestYankRetractsWhatThePlanListed(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}
	options, goMod := yankOptionsFor(t, recorder)
	actions, err := ObserveYank(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}

	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	result, err := Yank(t.Context(), &plandiff.File{Tag: "v1.2.3", Actions: actions}, options, logf)
	if err != nil || result == nil {
		t.Fatalf("Yank = %v, %v", result, err)
	}
	if got := readText(t, goMod); !strings.Contains(got, "v1.2.3 // bad") {
		t.Errorf("go.mod was not retracted:\n%s", got)
	}
}

func TestYankSaysWhatAnEarlierApplyAlreadyDid(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}
	options, _ := yankOptionsFor(t, recorder)
	actions, err := ObserveYank(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}

	// An earlier apply of the same plan already retracted go.mod.
	file := &plandiff.File{Tag: "v1.2.3", Actions: actions}
	if _, err := Yank(t.Context(), file, options, discard); err != nil {
		t.Fatal(err)
	}
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	if _, err := Yank(t.Context(), file, options, logf); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "gomod go.mod is already as planned") {
		t.Errorf("output = %q", got)
	}
}

func TestYankRefusesAPlanThatWentStale(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3", Body: "notes"}
	options, goMod := yankOptionsFor(t, recorder)
	actions, err := ObserveYank(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(goMod, []byte("module example.com/demo\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Yank(t.Context(), &plandiff.File{Tag: "v1.2.3", Actions: actions}, options, discard)
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "letsgo plan -yank v1.2.3 -out") {
		t.Errorf("err = %v", err)
	}
	if got := readText(t, goMod); strings.Contains(got, "retract") {
		t.Errorf("a stale plan was applied:\n%s", got)
	}
}

func TestYankReportsAForgeItCannotRead(t *testing.T) {
	options, _ := yankOptionsFor(t, publish.NewRecorder(nil))
	options.GoMod = filepath.Join(t.TempDir(), "go.mod")
	if _, err := Yank(t.Context(), &plandiff.File{Tag: "v1.2.3"}, options, discard); err == nil {
		t.Error("Yank went ahead with no go.mod to read")
	}
}

func TestTouchesFindsAKindOfAction(t *testing.T) {
	actions := []plandiff.Action{{Kind: plandiff.KindGoMod}}
	if !Touches(actions, plandiff.KindGoMod) || Touches(actions, plandiff.KindTap) {
		t.Error("Touches does not tell a go.mod action from a tap one")
	}
}
