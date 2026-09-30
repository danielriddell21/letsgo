package brew_test

import (
	"context"
	"errors"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

func nextSample(version string) brew.Formula {
	f := sample()
	f.Name, f.Version = brew.NextName("my-tool"), version
	return f
}

// A path that has never been published has nothing to be older than, so the
// first release of any kind advances it.
func TestPublishNextAdvancesAFormulaThatHasNeverBeenPublished(t *testing.T) {
	tap := &fakeTap{}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.PublishNext(context.Background(), tap, repo, nextSample("1.3.0-rc.1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Created || result.Path != "Formula/my-tool@next.rb" {
		t.Fatalf("result = %+v", result)
	}
	if len(tap.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(tap.writes))
	}
}

// An existing @next older than the release is overwritten: the ordinary
// rc -> stable progression.
func TestPublishNextOverwritesAnOlderFormula(t *testing.T) {
	old := nextSample("1.3.0-rc.1")
	content, err := old.Render()
	if err != nil {
		t.Fatal(err)
	}
	tap := &fakeTap{files: map[string][]byte{old.FileName(): content}}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.PublishNext(context.Background(), tap, repo, nextSample("1.3.0"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Updated {
		t.Fatalf("result = %+v, want updated", result)
	}
}

// A backport releasing behind an already-published @next must not roll it
// back.
func TestPublishNextKeepsAFormulaNewerThanTheRelease(t *testing.T) {
	ahead := nextSample("1.3.0")
	content, err := ahead.Render()
	if err != nil {
		t.Fatal(err)
	}
	tap := &fakeTap{files: map[string][]byte{ahead.FileName(): content}}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.PublishNext(context.Background(), tap, repo, nextSample("1.2.9"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Kept {
		t.Fatalf("result = %+v, want kept", result)
	}
	if len(tap.writes) != 0 {
		t.Errorf("a formula that was already newer was written anyway: %d writes", len(tap.writes))
	}
}

// An @next equal to the release is also left alone: only strictly newer
// counts as an advance, so a re-run of the same release does not churn the
// tap.
func TestPublishNextKeepsAnEqualFormula(t *testing.T) {
	same := nextSample("1.3.0")
	content, err := same.Render()
	if err != nil {
		t.Fatal(err)
	}
	tap := &fakeTap{files: map[string][]byte{same.FileName(): content}}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.PublishNext(context.Background(), tap, repo, nextSample("1.3.0"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Kept {
		t.Fatalf("result = %+v, want kept", result)
	}
}

// A file at the path that isn't a formula this package wrote (no readable
// version line) is left alone rather than guessed at.
func TestPublishNextKeepsAnUnparseableFormula(t *testing.T) {
	tap := &fakeTap{files: map[string][]byte{"Formula/my-tool@next.rb": []byte("not a formula\n")}}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.PublishNext(context.Background(), tap, repo, nextSample("1.3.0"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Kept {
		t.Fatalf("result = %+v, want kept", result)
	}
	if len(tap.writes) != 0 {
		t.Errorf("an unparseable formula was overwritten anyway: %d writes", len(tap.writes))
	}
}

// Retracting the release @next names points it back at the previous one.
func TestRevertNextRollsBackAFormulaThatNamesTheRetractedRelease(t *testing.T) {
	yanked := nextSample("1.3.0")
	content, err := yanked.Render()
	if err != nil {
		t.Fatal(err)
	}
	tap := &fakeTap{files: map[string][]byte{yanked.FileName(): content}}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.RevertNext(context.Background(), tap, repo, nextSample("1.2.0"), "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Updated || len(tap.writes) != 1 {
		t.Fatalf("result = %+v, writes = %d, want one update", result, len(tap.writes))
	}
}

// An @next that has moved on, is absent, or is not ours is not this release's
// to undo.
func TestRevertNextLeavesAFormulaThatDoesNotNameTheRetractedRelease(t *testing.T) {
	newer := nextSample("1.4.0")
	content, err := newer.Render()
	if err != nil {
		t.Fatal(err)
	}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	for name, files := range map[string]map[string][]byte{
		"moved on":    {newer.FileName(): content},
		"absent":      nil,
		"unparseable": {newer.FileName(): []byte("not a formula\n")},
	} {
		t.Run(name, func(t *testing.T) {
			tap := &fakeTap{files: files}
			result, err := brew.RevertNext(context.Background(), tap, repo, nextSample("1.2.0"), "1.3.0")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != brew.Kept || len(tap.writes) != 0 {
				t.Errorf("result = %+v, writes = %d, want it left alone", result, len(tap.writes))
			}
		})
	}
}

func TestRevertNextReportsATapThatCannotBeRead(t *testing.T) {
	tap := &fakeTap{err: errors.New("tap down")}
	_, err := brew.RevertNext(context.Background(), tap, github.Repo{}, nextSample("1.2.0"), "1.3.0")
	if err == nil {
		t.Fatal("RevertNext succeeded against an unreadable tap")
	}
}
