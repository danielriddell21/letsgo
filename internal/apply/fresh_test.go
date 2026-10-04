package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/manifest"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func TestFreshAgainstAcceptsAForgeThatHasNotMoved(t *testing.T) {
	targets := diffFixture(t)
	saved, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}

	file := &plandiff.File{Actions: saved}
	writes, err := Fresh(t.Context(), file, targets, discard)
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

	_, err = Fresh(t.Context(), &plandiff.File{Actions: saved}, targets, discard)
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
	if got := AlreadyDone(saved, done); len(got) != 1 || got[0] != "release v1.2.3" {
		t.Fatalf("AlreadyDone = %v, want the release", got)
	}

	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	if _, err := Fresh(t.Context(), &plandiff.File{Actions: saved}, targets, logf); err != nil {
		t.Errorf("a half-finished apply was refused: %v", err)
	}
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "release v1.2.3 is already as planned") {
		t.Errorf("output = %q", out)
	}
}

func TestStalenessSaysHowToRecover(t *testing.T) {
	saved := []plandiff.Action{{Op: plandiff.Add, Kind: plandiff.KindAsset, Target: "a", Planned: "sha256:a"}}
	current := []plandiff.Action{{Op: plandiff.Change, Kind: plandiff.KindAsset, Target: "a", Observed: "sha256:z", Planned: "sha256:a"}}

	err := Stale(saved, current, "letsgo plan -out")
	if err == nil || !strings.Contains(err.Error(), "letsgo plan -out") || !strings.Contains(err.Error(), "asset a") {
		t.Errorf("err = %v", err)
	}
	if err := Stale(saved, saved, "letsgo plan -out"); err != nil {
		t.Errorf("unchanged forge: %v", err)
	}
}

func TestGuardApplyReturnsClientsThatHoldToThePlan(t *testing.T) {
	targets := diffFixture(t)
	saved, err := publication.Observe(t.Context(), targets)
	if err != nil {
		t.Fatal(err)
	}

	forge, tap, err := Guard(t.Context(), &plandiff.File{Actions: saved}, targets, discard)
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
	if _, _, err := Guard(t.Context(), &plandiff.File{Actions: saved}, targets, discard); err == nil {
		t.Error("a stale plan was guarded instead of refused")
	}
}

func discard(string, ...any) {}

// diffFixture is a release about to be published to an empty forge.
func diffFixture(t *testing.T) publication.Options {
	t.Helper()
	p := &plan.Plan{
		Version: "1.2.3",
		Tag:     "v1.2.3",
		Repo:    discover.Repo{Host: "github.com", Owner: "you", Name: "foo"},
		HasRepo: true,
		Config:  &config.Config{},
		Tap:     github.Repo{Owner: "you", Name: "homebrew-tap"},
	}
	recorder := publish.NewRecorder(nil)
	return publication.Options{
		Plan: p, Forge: recorder, Tap: recorder, Repo: github.Repo{Owner: "you", Name: "foo"},
		Dir: t.TempDir(), Result: &release.Result{
			Artifacts: []build.Artifact{{
				Archive: "foo_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", ArchiveSHA256: "a1",
				Binaries: []build.Binary{{Name: "foo"}},
			}},
			Manifest: &manifest.Manifest{
				Version: "1.2.3", Tag: "v1.2.3",
				Artifacts: []manifest.Artifact{{
					Name: "foo_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "a1",
					Binary: "foo",
				}},
			},
		},
	}
}

type brokenForge struct{ publish.Forge }

func (brokenForge) ReleaseByTag(context.Context, github.Repo, string) (*github.Release, error) {
	return nil, errors.New("forge is down")
}

func TestFreshAndGuardReportAForgeThatCannotBeRead(t *testing.T) {
	targets := diffFixture(t)
	targets.Forge = brokenForge{}
	file := &plandiff.File{}

	if _, err := Fresh(t.Context(), file, targets, discard); err == nil || !strings.Contains(err.Error(), "forge is down") {
		t.Errorf("Fresh: err = %v", err)
	}
	if _, _, err := Guard(t.Context(), file, targets, discard); err == nil {
		t.Error("Guard accepted a forge it could not read")
	}
}

func TestGuardWithNoPlanLeavesTheClientsAlone(t *testing.T) {
	targets := diffFixture(t)
	forge, tap, err := Guard(t.Context(), nil, targets, discard)
	if err != nil || forge != targets.Forge || tap != targets.Tap {
		t.Errorf("Guard(nil) = %v, %v, %v", forge, tap, err)
	}
}

func TestAlreadyDoneIgnoresWhatThePlanKeeps(t *testing.T) {
	kept := []plandiff.Action{{Op: plandiff.Keep, Kind: plandiff.KindAsset, Target: "a"}}
	if got := AlreadyDone(kept, kept); got != nil {
		t.Errorf("AlreadyDone = %v, want nothing", got)
	}
}

func TestHoldWithNoPlanHasNothingToHoldTo(t *testing.T) {
	if err := Hold(nil, nil, nil, discard); err != nil {
		t.Errorf("Hold(nil) = %v", err)
	}
}

func TestShort12LeavesAShortValueAlone(t *testing.T) {
	if got := Short12("abc"); got != "abc" {
		t.Errorf("Short12 = %q", got)
	}
	if got := Short12("0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("Short12 = %q", got)
	}
}
