package plan_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/plan"
)

const (
	digestA = "sha256:ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12"
	digestB = "sha256:77e0aa11bb22cc3377e0aa11bb22cc3377e0aa11bb22cc3377e0aa11bb22cc33"
)

func TestCountsLeavesOutWhatIsKept(t *testing.T) {
	actions := []plan.Action{
		{Op: plan.Add}, {Op: plan.Add}, {Op: plan.Change}, {Op: plan.Remove}, {Op: plan.Keep}, {Op: plan.Keep},
	}

	add, change, remove := plan.Counts(actions)
	if add != 2 || change != 1 || remove != 1 {
		t.Errorf("Counts = %d, %d, %d; want 2, 1, 1", add, change, remove)
	}
	if got, want := plan.Summary(actions), "Plan: 2 to add, 1 to change, 1 to remove."; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestRenderDrawsOneLinePerActionAndAFooter(t *testing.T) {
	got := plan.Render([]plan.Action{
		{Op: plan.Add, Kind: plan.KindRelease, Target: "v1.3.0", Planned: digestA},
		{Op: plan.Add, Kind: plan.KindAsset, Target: "gambit_1.3.0_linux_amd64.tar.gz", Planned: digestB},
		{Op: plan.Change, Kind: plan.KindTap, Target: "Formula/gambit.rb", Observed: "blob:9c1e0000000000000000", Planned: "blob:3d7a0000000000000000"},
		{Op: plan.Keep, Kind: plan.KindImage, Target: "ghcr.io/you/gambit:1", Observed: digestA, Planned: digestA},
		{Op: plan.Add, Kind: plan.KindProxy, Target: "example.com/gambit@v1.3.0"},
	})

	want := `  + release  v1.3.0                           sha256:ab12…
  + asset    gambit_1.3.0_linux_amd64.tar.gz  sha256:77e0…
  ~ tap      Formula/gambit.rb                blob:9c1e… → blob:3d7a…
  = image    ghcr.io/you/gambit:1             (already sha256:ab12…)
  + proxy    example.com/gambit@v1.3.0        absent

  Plan: 3 to add, 1 to change, 0 to remove.
`
	if got != want {
		t.Errorf("Render:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderOfNothingIsJustTheFooter(t *testing.T) {
	got := plan.Render(nil)
	if strings.TrimSpace(got) != "Plan: 0 to add, 0 to change, 0 to remove." {
		t.Errorf("Render(nil) = %q", got)
	}
}

func TestRenderShowsARemovalByWhatItRemoves(t *testing.T) {
	got := plan.Render([]plan.Action{{Op: plan.Remove, Kind: plan.KindAsset, Target: "old.zip", Observed: digestA}})
	if !strings.Contains(got, "- asset  old.zip  sha256:ab12…") {
		t.Errorf("removal is not shown by its observed state:\n%s", got)
	}
}

func TestMarkdownPutsMarkersInColumnZeroOfADiffBlock(t *testing.T) {
	got := plan.Markdown("letsgo plan", []plan.Action{
		{Op: plan.Add, Kind: plan.KindAsset, Target: "a.tar.gz", Planned: "sha256:ab"},
		{Op: plan.Change, Kind: plan.KindTap, Target: "Formula/a.rb", Observed: "blob:1", Planned: "blob:2"},
		{Op: plan.Remove, Kind: plan.KindImage, Target: "a:old", Observed: "sha256:cd"},
		{Op: plan.Keep, Kind: plan.KindProxy, Target: "example.com/a", Planned: "v1"},
	})

	want := "## letsgo plan\n\n" +
		"```diff\n" +
		"+ asset  a.tar.gz      sha256:ab\n" +
		"! tap    Formula/a.rb  blob:1 → blob:2\n" +
		"- image  a:old         sha256:cd\n" +
		"```\n\n" +
		"Plan: 1 to add, 1 to change, 1 to remove. 1 unchanged.\n"
	if got != want {
		t.Errorf("Markdown =\n%s\nwant\n%s", got, want)
	}
}

func TestMarkdownOmitsTheBlockWhenNothingChanges(t *testing.T) {
	got := plan.Markdown("letsgo plan", []plan.Action{{Op: plan.Keep, Kind: plan.KindProxy, Target: "x", Planned: "v1"}})
	want := "## letsgo plan\n\nPlan: 0 to add, 0 to change, 0 to remove. 1 unchanged.\n"
	if got != want {
		t.Errorf("Markdown = %q, want %q", got, want)
	}
	if got := plan.Markdown("t", nil); strings.Contains(got, "```") || strings.Contains(got, "unchanged") {
		t.Errorf("an empty plan renders a block or a kept count: %q", got)
	}
}
