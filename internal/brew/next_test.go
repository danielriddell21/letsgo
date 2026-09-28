package brew_test

import (
	"context"
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
