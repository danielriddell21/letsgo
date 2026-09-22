package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
)

func releasePlan() *plan.Plan {
	return &plan.Plan{
		Version: "1.2.3",
		Tag:     "v1.2.3",
		Repo:    discover.Repo{Host: "github.com", Owner: "you", Name: "foo"},
		HasRepo: true,
		Config:  &config.Config{},
	}
}

func built(artifacts ...build.Artifact) *release.Result {
	return &release.Result{Artifacts: artifacts}
}

func artifact(archive, goos, goarch, sum string, binaries ...string) build.Artifact {
	built := make([]build.Binary, len(binaries))
	for i, b := range binaries {
		built[i] = build.Binary{Name: b}
	}
	return build.Artifact{
		Archive: archive, OS: goos, Arch: goarch, ArchiveSHA256: sum, Binaries: built,
	}
}

func TestFormulasGroupsByArchive(t *testing.T) {
	// Two archives produce two formulas: a formula names one archive per
	// platform, so alpha and beta cannot share one.
	result := built(
		artifact("alpha_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "alpha"),
		artifact("beta_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "b1", "beta"),
		artifact("alpha_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "a2", "alpha"),
	)

	got := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 2 {
		t.Fatalf("got %d formulas, want 2", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Errorf("formulas = %s, %s", got[0].Name, got[1].Name)
	}
	if len(got[0].Platforms) != 2 {
		t.Errorf("alpha has %d platforms, want 2", len(got[0].Platforms))
	}

	// The URL has to name the release the archives were published under.
	if !strings.Contains(got[0].Platforms[0].URL, "/releases/download/v1.2.3/alpha_1.2.3_linux_amd64.tar.gz") {
		t.Errorf("url = %q", got[0].Platforms[0].URL)
	}
	if got[0].Platforms[0].SHA256 != "a1" {
		t.Errorf("digest = %q", got[0].Platforms[0].SHA256)
	}
}

// desc and license come from the repository rather than from config, and are
// omitted when it has none: inventing either is worse than leaving them out.
func TestFormulasTakeMetadataFromTheRepository(t *testing.T) {
	result := built(artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "", "foo"))

	without := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)
	if without[0].Description != "" || without[0].License != "" {
		t.Errorf("metadata was invented: %+v", without[0])
	}
	if without[0].Homepage != "https://github.com/you/foo" {
		t.Errorf("homepage = %q", without[0].Homepage)
	}

	with := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, &github.RepoInfo{
		Description: "a tool", License: "MIT", Homepage: "https://foo.example",
	})
	if with[0].Description != "a tool" || with[0].License != "MIT" {
		t.Errorf("metadata = %+v", with[0])
	}
	if with[0].Homepage != "https://foo.example" {
		t.Errorf("homepage = %q", with[0].Homepage)
	}
}

func TestForced(t *testing.T) {
	for _, c := range []struct {
		major, minor, patch bool
		want                bump.Level
	}{
		{true, false, false, bump.Major},
		{false, true, false, bump.Minor},
		{false, false, true, bump.Patch},
		{false, false, false, bump.None},
		// Several at once resolves to the largest, which is the only reading
		// that cannot under-bump.
		{true, true, true, bump.Major},
	} {
		if got := forced(c.major, c.minor, c.patch); got != c.want {
			t.Errorf("forced(%v,%v,%v) = %v, want %v", c.major, c.minor, c.patch, got, c.want)
		}
	}
}

// An archive holding several tools is one formula that installs all of them,
// not one formula per tool fighting over the same file. Without this, a
// collection whose commands include a name Homebrew core already uses would
// write a formula that shadows it.
func TestFormulasInstallEveryBinaryInTheArchive(t *testing.T) {
	result := built(
		artifact("toolshed_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "crabs", "duck", "fish"),
		artifact("toolshed_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "a2", "crabs", "duck", "fish"),
	)

	got := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 1 {
		t.Fatalf("got %d formulas, want 1", len(got))
	}
	if got[0].Name != "toolshed" {
		t.Errorf("name = %q, want the archive's", got[0].Name)
	}
	if strings.Join(got[0].Binaries, ",") != "crabs,duck,fish" {
		t.Errorf("binaries = %q", got[0].Binaries)
	}

	rendered, err := got[0].Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), `bin.install "crabs", "duck", "fish"`) {
		t.Errorf("install line missing from:\n%s", rendered)
	}
}

// A variant is a second product from the same source, and a windowed build
// usually belongs in a cask. Writing it a formula anyway would put a package
// in the tap that the repository never asked for.
func TestFormulasLeaveOutAVariant(t *testing.T) {
	p := releasePlan()
	p.Groups = []plan.Group{
		{Name: "foo"},
		{Name: "foo-gui", Variant: "gui"},
	}

	result := built(
		artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"),
		artifact("foo_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "a2", "foo"),
		artifact("foo-gui_1.2.3_darwin_arm64.tar.gz", "darwin", "arm64", "g1", "foo"),
	)

	got := formulas(p, result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 1 {
		names := make([]string, len(got))
		for i, f := range got {
			names[i] = f.Name
		}
		t.Fatalf("got formulas %v, want only foo", names)
	}
	if got[0].Name != "foo" {
		t.Errorf("formula = %q, want foo", got[0].Name)
	}
	// The variant's darwin archive must not be folded into the release's
	// formula either: it is a different build of the same command.
	if len(got[0].Platforms) != 2 {
		t.Errorf("foo has %d platforms, want the release's two", len(got[0].Platforms))
	}
	for _, p := range got[0].Platforms {
		if strings.Contains(p.URL, "foo-gui") {
			t.Errorf("the variant's archive leaked into the formula: %q", p.URL)
		}
	}
}

// Caveats come from the config rather than the repository, because they say
// what the program needs of the machine — which no API knows.
func TestFormulasCarryTheConfiguredCaveats(t *testing.T) {
	p := releasePlan()
	p.Config.BrewCaveats = "needs a display"

	got := formulas(p, built(
		artifact("foo_1.2.3_linux_amd64.tar.gz", "linux", "amd64", "a1", "foo"),
	), github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 1 {
		t.Fatalf("got %d formulas, want 1", len(got))
	}
	if got[0].Caveats != "needs a display" {
		t.Errorf("Caveats = %q", got[0].Caveats)
	}
}

// The release client is reused when no tap token is configured. A second
// client holding the same token would mean a second connection pool for no
// reason, and it would make the single-credential arrangement look like a
// different code path than it is.
func TestTapClientForReusesTheReleaseClient(t *testing.T) {
	t.Setenv("LETSGO_TAP_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	client := github.New("release-token")
	client.UserAgent = "letsgo/test"

	if got := tapClientFor(client, "", "release-token"); got != client {
		t.Error("a tap with no token of its own got a second client")
	}
}

func TestTapClientForSplitsWhenTheTapHasItsOwnToken(t *testing.T) {
	t.Setenv("LETSGO_TAP_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	client := github.New("release-token")
	client.UserAgent = "letsgo/test"

	got := tapClientFor(client, "tap-token", "release-token")
	if got == client {
		t.Fatal("the tap token did not produce a client of its own")
	}
	// The user agent has to carry over, or the tap's requests arrive
	// unidentified and GitHub is entitled to refuse them.
	if got.UserAgent != client.UserAgent {
		t.Errorf("UserAgent = %q, want %q", got.UserAgent, client.UserAgent)
	}
}

// The environment is read when the flag is absent, which is how CI passes it.
func TestTapClientForReadsTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("LETSGO_TAP_TOKEN", "from-env")

	client := github.New("release-token")
	if got := tapClientFor(client, "", "release-token"); got == client {
		t.Error("LETSGO_TAP_TOKEN did not produce a client of its own")
	}
}
