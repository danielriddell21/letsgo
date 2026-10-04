package forgerelease

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/plan"
)

func observed(t *testing.T, existing *github.Release, notes NotesMode, body string) []plan.Action {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.tgz"), []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(nil)
	recorder.Existing = existing

	actions, err := Observe(t.Context(), Options{
		Client: recorder, Repo: github.Repo{Owner: "you", Name: "tool"}, Dir: dir,
		Files: []string{"a.tgz"}, Sums: map[string]string{"a.tgz": "abc"}, Notes: notes,
		Release: github.ReleaseInput{TagName: "v1.0.0", Body: body},
	})
	if err != nil {
		t.Fatal(err)
	}
	return actions
}

func wantOps(t *testing.T, actions []plan.Action, ops ...plan.Op) {
	t.Helper()
	if len(actions) != len(ops) {
		t.Fatalf("actions = %+v, want %d", actions, len(ops))
	}
	for i, op := range ops {
		if actions[i].Op != op {
			t.Errorf("action %d = %+v, want op %q", i, actions[i], op)
		}
	}
}

func TestObserveAddsEverythingForANewRelease(t *testing.T) {
	actions := observed(t, nil, NotesReplace, "notes")
	wantOps(t, actions, plan.Add, plan.Add)
	if actions[1].Planned != "sha256:abc" {
		t.Errorf("planned = %q", actions[1].Planned)
	}
}

func TestObserveKeepsWhatIsAlreadyCorrect(t *testing.T) {
	existing := &github.Release{ID: 1, Body: "notes", Assets: []github.Asset{{Name: "a.tgz", Size: 4, Digest: "sha256:abc"}}}
	wantOps(t, observed(t, existing, NotesReplace, "notes"), plan.Keep, plan.Keep)
}

func TestObserveChangesWhatDiffers(t *testing.T) {
	existing := &github.Release{ID: 1, Body: "old", Assets: []github.Asset{{Name: "a.tgz", Size: 4, Digest: "sha256:zzz"}}}
	actions := observed(t, existing, NotesReplace, "notes")
	wantOps(t, actions, plan.Change, plan.Change)
	if actions[1].Observed != "sha256:zzz" {
		t.Errorf("observed = %q", actions[1].Observed)
	}
}

func TestObserveAddsAMissingAssetToAnExistingRelease(t *testing.T) {
	existing := &github.Release{ID: 1, Body: "notes"}
	wantOps(t, observed(t, existing, NotesReplace, "notes"), plan.Keep, plan.Add)
}

func TestObserveAppendingChangesTheNotesOnlyWhenSomethingIsGenerated(t *testing.T) {
	existing := &github.Release{ID: 1, Body: "hand written", Assets: []github.Asset{{Name: "a.tgz", Size: 4, Digest: "sha256:abc"}}}
	wantOps(t, observed(t, existing, NotesAppend, "generated"), plan.Change, plan.Keep)
	wantOps(t, observed(t, existing, NotesAppend, ""), plan.Keep, plan.Keep)
}
