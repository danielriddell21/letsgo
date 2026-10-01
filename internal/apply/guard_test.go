package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func guardFor(t *testing.T, actions ...plandiff.Action) (publish.Forge, brew.FileAPI, *publish.Recorder) {
	t.Helper()
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 1}
	writes := NewWrites(actions)
	return writes.Forge(recorder, "v1.2.3"), writes.Tap(recorder), recorder
}

func TestGuardedForgeAllowsWhatThePlanLists(t *testing.T) {
	forge, tap, recorder := guardFor(t,
		plandiff.Action{Op: plandiff.Add, Kind: plandiff.KindRelease, Target: "v1.2.3"},
		plandiff.Action{Op: plandiff.Change, Kind: plandiff.KindAsset, Target: "a.tgz"},
		plandiff.Action{Op: plandiff.Add, Kind: plandiff.KindTap, Target: "Formula/foo.rb"},
	)
	ctx, repo := t.Context(), github.Repo{Owner: "you", Name: "foo"}

	if _, err := forge.CreateRelease(ctx, repo, github.ReleaseInput{TagName: "v1.2.3"}); err != nil {
		t.Errorf("CreateRelease: %v", err)
	}
	if _, err := forge.UpdateRelease(ctx, repo, 1, github.ReleaseInput{TagName: "v1.2.3"}); err != nil {
		t.Errorf("UpdateRelease: %v", err)
	}
	if err := forge.DeleteAsset(ctx, repo, 7); err != nil {
		t.Errorf("DeleteAsset: %v", err)
	}
	if _, err := forge.UploadAsset(ctx, repo, 1, "a.tgz", 0, strings.NewReader("")); err != nil {
		t.Errorf("UploadAsset: %v", err)
	}
	if err := tap.WriteFile(ctx, repo, github.FileInput{Path: "Formula/foo.rb"}); err != nil {
		t.Errorf("WriteFile: %v", err)
	}
	if len(recorder.Calls) == 0 {
		t.Error("nothing reached the forge")
	}
}

func TestGuardedForgeRefusesWhatThePlanDoesNotList(t *testing.T) {
	// The plan leaves the release and its one asset as they are.
	forge, tap, recorder := guardFor(t,
		plandiff.Action{Op: plandiff.Keep, Kind: plandiff.KindAsset, Target: "a.tgz"},
	)
	ctx, repo := t.Context(), github.Repo{Owner: "you", Name: "foo"}

	for name, err := range map[string]error{
		"create":  second(forge.CreateRelease(ctx, repo, github.ReleaseInput{})),
		"update":  second(forge.UpdateRelease(ctx, repo, 1, github.ReleaseInput{})),
		"delete":  forge.DeleteAsset(ctx, repo, 7),
		"upload":  second(forge.UploadAsset(ctx, repo, 1, "b.tgz", 0, strings.NewReader(""))),
		"kept":    second(forge.UploadAsset(ctx, repo, 1, "a.tgz", 0, strings.NewReader(""))),
		"tapfile": tap.WriteFile(ctx, repo, github.FileInput{Path: "Formula/foo.rb"}),
	} {
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if len(recorder.Calls) != 0 {
		t.Errorf("a refused write reached the forge: %v", recorder.Calls)
	}
}

func second[T any](_ T, err error) error { return err }

func TestGuardYankRefusesWhatThePlanDidNotList(t *testing.T) {
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 7, TagName: "v1.2.3"}
	repo := github.Repo{Owner: "you", Name: "demo"}
	nothing := NewWrites(nil)

	guarded := nothing.Yank(yank.Options{Client: recorder, TapAPI: recorder, Tag: "v1.2.3"})
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
	listed := NewWrites([]plandiff.Action{
		{Op: plandiff.Change, Kind: plandiff.KindRelease, Target: "v1.2.3"},
		{Op: plandiff.Change, Kind: plandiff.KindGoMod, Target: "go.mod"},
		{Op: plandiff.Add, Kind: plandiff.KindTap, Target: "Formula/x.rb"},
	})

	guarded := listed.Yank(yank.Options{Client: recorder, TapAPI: recorder, Tag: "v1.2.3"})
	if _, err := guarded.Client.UpdateRelease(context.Background(), repo, 7, github.ReleaseInput{}); err != nil {
		t.Error(err)
	}
	if err := guarded.TapAPI.WriteFile(context.Background(), repo, github.FileInput{Path: "Formula/x.rb"}); err != nil {
		t.Error(err)
	}
	if err := guarded.WriteGoMod(goMod, []byte("module x\n")); err != nil {
		t.Error(err)
	}
	if got, err := os.ReadFile(goMod); err != nil || string(got) != "module x\n" {
		t.Errorf("go.mod = %q, %v", got, err)
	}
}
