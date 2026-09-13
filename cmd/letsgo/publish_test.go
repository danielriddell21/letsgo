package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bump"
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
	}
}

func built(artifacts ...build.Artifact) *release.Result {
	return &release.Result{Artifacts: artifacts}
}

func TestFormulasGroupsByBinary(t *testing.T) {
	// A module with two commands produces two formulas: a formula names one
	// archive per platform, so alpha and beta cannot share one.
	result := built(
		build.Artifact{Binary: "alpha", OS: "linux", Arch: "amd64", Archive: "alpha_linux.tar.gz", ArchiveSHA256: "a1"},
		build.Artifact{Binary: "beta", OS: "linux", Arch: "amd64", Archive: "beta_linux.tar.gz", ArchiveSHA256: "b1"},
		build.Artifact{Binary: "alpha", OS: "darwin", Arch: "arm64", Archive: "alpha_darwin.tar.gz", ArchiveSHA256: "a2"},
	)

	got := formulas(releasePlan(), result, github.Repo{Owner: "you", Name: "foo"}, nil)

	if len(got) != 2 {
		t.Fatalf("got %d formulas, want 2", len(got))
	}
	if got[0].Binary != "alpha" || got[1].Binary != "beta" {
		t.Errorf("formulas = %s, %s", got[0].Binary, got[1].Binary)
	}
	if len(got[0].Platforms) != 2 {
		t.Errorf("alpha has %d platforms, want 2", len(got[0].Platforms))
	}

	// The URL has to name the release the archives were published under.
	if !strings.Contains(got[0].Platforms[0].URL, "/releases/download/v1.2.3/alpha_linux.tar.gz") {
		t.Errorf("url = %q", got[0].Platforms[0].URL)
	}
	if got[0].Platforms[0].SHA256 != "a1" {
		t.Errorf("digest = %q", got[0].Platforms[0].SHA256)
	}
}

// desc and license come from the repository rather than from config, and are
// omitted when it has none: inventing either is worse than leaving them out.
func TestFormulasTakeMetadataFromTheRepository(t *testing.T) {
	result := built(build.Artifact{Binary: "foo", OS: "linux", Arch: "amd64", Archive: "foo.tar.gz"})

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
