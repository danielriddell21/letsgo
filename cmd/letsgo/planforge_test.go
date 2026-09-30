package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func TestRunPlanDiffHasNoJSONForm(t *testing.T) {
	err := runPlan([]string{"--diff", "--json"})
	if err == nil || !strings.Contains(err.Error(), "--diff") {
		t.Errorf("err = %v", err)
	}
}

func diffFixture(t *testing.T) publication.Options {
	t.Helper()
	p := releasePlan()
	p.Tap = github.Repo{Owner: "you", Name: "homebrew-tap"}

	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"))
	result.Manifest = &manifest.Manifest{}

	recorder := publish.NewRecorder(nil)
	return publication.Options{
		Plan: p, Forge: recorder, Tap: recorder, Repo: github.Repo{Owner: "you", Name: "foo"},
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
