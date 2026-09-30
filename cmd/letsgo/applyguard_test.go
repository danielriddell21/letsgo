package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func guardFor(t *testing.T, actions ...plandiff.Action) (guardedForge, guardedTap, *publish.Recorder) {
	t.Helper()
	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{ID: 1}
	writes := newPlannedWrites(actions)
	return guardedForge{Forge: recorder, tag: "v1.2.3", writes: writes}, guardedTap{FileAPI: recorder, writes: writes}, recorder
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

func TestFreshAgainstAcceptsAForgeThatHasNotMoved(t *testing.T) {
	p, targets := diffFixture(t)
	saved, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}

	file := &plandiff.File{Actions: saved}
	writes, err := freshAgainst(t.Context(), p, file, targets)
	if err != nil {
		t.Fatal(err)
	}
	if writes.allows(plandiff.KindRelease, "v1.2.3") != nil || writes.allows(plandiff.KindRelease, "v9") == nil {
		t.Errorf("writes = %+v", writes.pending)
	}
}

func TestFreshAgainstRefusesAHandEditedTapFile(t *testing.T) {
	p, targets := diffFixture(t)
	saved, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}

	// Between the plan and the apply, someone edits the formula by hand.
	targets.Tap.(*publish.Recorder).Files = map[string][]byte{"Formula/foo.rb": []byte("by hand")}

	_, err = freshAgainst(t.Context(), p, &plandiff.File{Actions: saved}, targets)
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "Formula/foo.rb") {
		t.Errorf("err = %v, want a stale plan naming Formula/foo.rb", err)
	}
}

func TestFreshAgainstResumesAnApplyThatStoppedHalfway(t *testing.T) {
	p, targets := diffFixture(t)
	saved, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}

	// The earlier apply created the release and wrote the formula, then stopped.
	recorder := targets.Forge.(*publish.Recorder)
	recorder.Existing = &github.Release{ID: 1, TagName: "v1.2.3"}
	done, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}
	if got := alreadyDone(saved, done); len(got) != 1 || got[0] != "release v1.2.3" {
		t.Fatalf("alreadyDone = %v, want the release", got)
	}

	out := captureStdout(t, func() {
		if _, err := freshAgainst(t.Context(), p, &plandiff.File{Actions: saved}, targets); err != nil {
			t.Errorf("a half-finished apply was refused: %v", err)
		}
	})
	if !strings.Contains(out, "release v1.2.3 is already as planned") {
		t.Errorf("output = %q", out)
	}
}

func TestStalenessSaysHowToRecover(t *testing.T) {
	saved := []plandiff.Action{{Op: plandiff.Add, Kind: plandiff.KindAsset, Target: "a", Planned: "sha256:a"}}
	current := []plandiff.Action{{Op: plandiff.Change, Kind: plandiff.KindAsset, Target: "a", Observed: "sha256:z", Planned: "sha256:a"}}

	err := staleness(saved, current, "letsgo plan -out")
	if err == nil || !strings.Contains(err.Error(), "letsgo plan -out") || !strings.Contains(err.Error(), "asset a") {
		t.Errorf("err = %v", err)
	}
	if err := staleness(saved, saved, "letsgo plan -out"); err != nil {
		t.Errorf("unchanged forge: %v", err)
	}
}

func TestGuardApplyReturnsClientsThatHoldToThePlan(t *testing.T) {
	p, targets := diffFixture(t)
	saved, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}

	forge, tap, err := guardApply(t.Context(), p, &plandiff.File{Actions: saved}, targets)
	if err != nil {
		t.Fatal(err)
	}
	repo := github.Repo{Owner: "you", Name: "foo"}
	if _, err := forge.CreateRelease(t.Context(), repo, github.ReleaseInput{}); err != nil {
		t.Errorf("a planned write was refused: %v", err)
	}
	if err := tap.WriteFile(t.Context(), repo, github.FileInput{Path: "Formula/other.rb"}); err == nil {
		t.Error("an unplanned tap write was allowed")
	}

	targets.Tap.(*publish.Recorder).Files = map[string][]byte{"Formula/foo.rb": []byte("by hand")}
	if _, _, err := guardApply(t.Context(), p, &plandiff.File{Actions: saved}, targets); err == nil {
		t.Error("a stale plan was guarded instead of refused")
	}
}
