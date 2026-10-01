package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func TestFreshAgainstAcceptsAForgeThatHasNotMoved(t *testing.T) {
	targets := diffFixture(t)
	saved, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}

	file := &plandiff.File{Actions: saved}
	writes, err := freshAgainst(t.Context(), file, targets)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Allows(plandiff.KindRelease, "v1.2.3") != nil || writes.Allows(plandiff.KindRelease, "v9") == nil {
		t.Errorf("writes = %+v", writes)
	}
}

func TestFreshAgainstRefusesAHandEditedTapFile(t *testing.T) {
	targets := diffFixture(t)
	saved, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}

	// Between the plan and the apply, someone edits the formula by hand.
	targets.Tap.(*publish.Recorder).Files = map[string][]byte{"Formula/foo.rb": []byte("by hand")}

	_, err = freshAgainst(t.Context(), &plandiff.File{Actions: saved}, targets)
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "Formula/foo.rb") {
		t.Errorf("err = %v, want a stale plan naming Formula/foo.rb", err)
	}
}

func TestFreshAgainstResumesAnApplyThatStoppedHalfway(t *testing.T) {
	targets := diffFixture(t)
	saved, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}

	// The earlier apply created the release and wrote the formula, then stopped.
	recorder := targets.Forge.(*publish.Recorder)
	recorder.Existing = &github.Release{ID: 1, TagName: "v1.2.3"}
	done, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}
	if got := alreadyDone(saved, done); len(got) != 1 || got[0] != "release v1.2.3" {
		t.Fatalf("alreadyDone = %v, want the release", got)
	}

	out := captureStdout(t, func() {
		if _, err := freshAgainst(t.Context(), &plandiff.File{Actions: saved}, targets); err != nil {
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
	targets := diffFixture(t)
	saved, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}

	forge, tap, err := guardApply(t.Context(), &plandiff.File{Actions: saved}, targets)
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
	if _, _, err := guardApply(t.Context(), &plandiff.File{Actions: saved}, targets); err == nil {
		t.Error("a stale plan was guarded instead of refused")
	}
}
