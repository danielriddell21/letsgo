package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func observeTap(t *testing.T, existing map[string][]byte, writes map[string][]byte) []plandiff.Action {
	t.Helper()
	recorder := publish.NewRecorder(nil)
	recorder.Files = existing
	tap := &tapObserver{api: recorder}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	for path, content := range writes {
		file, err := tap.ReadFile(t.Context(), repo, path)
		if err != nil {
			t.Fatal(err)
		}
		if file != nil && string(file.Content) == string(content) {
			continue
		}
		if err := tap.WriteFile(t.Context(), repo, github.FileInput{Path: path, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	return tap.actions()
}

func TestTapObserverReportsAddChangeAndKeep(t *testing.T) {
	for _, tt := range []struct {
		name     string
		existing map[string][]byte
		want     plandiff.Op
	}{
		{"absent", nil, plandiff.Add},
		{"different", map[string][]byte{"Formula/tool.rb": []byte("old")}, plandiff.Change},
		{"identical", map[string][]byte{"Formula/tool.rb": []byte("new")}, plandiff.Keep},
	} {
		t.Run(tt.name, func(t *testing.T) {
			actions := observeTap(t, tt.existing, map[string][]byte{"Formula/tool.rb": []byte("new")})
			if len(actions) != 1 || actions[0].Op != tt.want || actions[0].Kind != plandiff.KindTap {
				t.Fatalf("actions = %+v, want one %q", actions, tt.want)
			}
		})
	}
}

func TestTapObserverRefusesAWriteWithoutARead(t *testing.T) {
	tap := &tapObserver{api: publish.NewRecorder(nil)}
	err := tap.WriteFile(t.Context(), github.Repo{}, github.FileInput{Path: "x"})
	if err == nil || !strings.Contains(err.Error(), "without being read") {
		t.Errorf("err = %v", err)
	}
}

func TestBlobFingerprintMatchesGit(t *testing.T) {
	// `printf 'hello\n' | git hash-object --stdin`
	want := "blob:ce013625030ba8dba906f756967f9e9ca394464a"
	if got := blobFingerprint([]byte("hello\n")); got != want {
		t.Errorf("blobFingerprint = %q, want %q", got, want)
	}
}

func TestRunPlanDiffHasNoJSONForm(t *testing.T) {
	err := runPlan([]string{"--diff", "--json"})
	if err == nil || !strings.Contains(err.Error(), "--diff") {
		t.Errorf("err = %v", err)
	}
}

func diffFixture(t *testing.T) (*plan.Plan, forgeTargets) {
	t.Helper()
	p := releasePlan()
	p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}

	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
	result.Manifest = &manifest.Manifest{}

	recorder := publish.NewRecorder(nil)
	return p, forgeTargets{
		Forge: recorder, Tap: recorder, Repo: github.Repo{Owner: "you", Name: "foo"},
		Dir: t.TempDir(), Result: result,
	}
}

func opsOf(actions []plandiff.Action) []plandiff.Op {
	ops := make([]plandiff.Op, len(actions))
	for i, a := range actions {
		ops[i] = a.Op
	}
	return ops
}

func TestDiffForgeAddsTheReleaseAndTheTapFiles(t *testing.T) {
	p, targets := diffFixture(t)

	actions, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}

	// The release, then the formula and its @next.
	want := []plandiff.Op{plandiff.Add, plandiff.Add, plandiff.Add}
	if got := opsOf(actions); !slices.Equal(got, want) {
		t.Errorf("ops = %v, want %v\n%+v", got, want, actions)
	}
}

func TestDiffForgeLeavesTheTapAloneForADraft(t *testing.T) {
	p, targets := diffFixture(t)
	p.Config.Draft = true

	actions, err := diffForge(t.Context(), p, targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Kind != plandiff.KindRelease {
		t.Errorf("actions = %+v, want the release only", actions)
	}
}
