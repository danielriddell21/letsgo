package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
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

// The flag has to survive the trip from the command line to the publishers:
// this drives a rehearsed release rather than the helpers it is made of.
func TestReleaseDraftFlagReachesTheTap(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantSkipped bool
	}{
		{"with --draft", []string{"-draft"}, true},
		{"without --draft", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emptyForge(t)
			// A formula needs a macOS or Linux build to install; a Windows
			// host has neither, so it is given one.
			config := "brew you/homebrew-tap\n"
			if runtime.GOOS == "windows" {
				config = "build linux/amd64\n" + config
			}
			t.Chdir(moduleFixtureWith(t, config, nil))

			args := append([]string{"-snapshot", "-o", filepath.Join(t.TempDir(), "dist")}, tc.args...)
			var err error
			out := captureStdout(t, func() { err = runRelease(args) })
			if err != nil {
				t.Fatalf("runRelease = %v\n%s", err, out)
			}
			if got := strings.Contains(out, "skipped the Homebrew tap"); got != tc.wantSkipped {
				t.Errorf("tap skipped = %v, want %v\n%s", got, tc.wantSkipped, out)
			}
		})
	}
}

func TestNoDraftFlagLeavesAConfiguredDraftAlone(t *testing.T) {
	p := releasePlan()
	p.Config.Draft = true

	applyDraftFlag(p, false)

	if !p.Config.Draft {
		t.Error("an absent --draft flag cleared draft = true from the config")
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

// releaseClientFor mirrors tapClientFor exactly, for the same reason: the
// single-credential arrangement must not change until a repository opts into
// publishing the release under a bot identity of its own.
func TestReleaseClientForReusesTheMainClient(t *testing.T) {
	t.Setenv("LETSGO_RELEASE_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	client := github.New("workflow-token")
	client.UserAgent = "letsgo/test"

	if got := releaseClientFor(client, "", "workflow-token"); got != client {
		t.Error("a release with no token of its own got a second client")
	}
}

func TestReleaseClientForSplitsWhenTheReleaseHasItsOwnToken(t *testing.T) {
	t.Setenv("LETSGO_RELEASE_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	client := github.New("workflow-token")
	client.UserAgent = "letsgo/test"

	got := releaseClientFor(client, "release-token", "workflow-token")
	if got == client {
		t.Fatal("the release token did not produce a client of its own")
	}
	// The user agent has to carry over, or the release's requests arrive
	// unidentified and GitHub is entitled to refuse them.
	if got.UserAgent != client.UserAgent {
		t.Errorf("UserAgent = %q, want %q", got.UserAgent, client.UserAgent)
	}
}

// The environment is read when the flag is absent, which is how CI passes it.
func TestReleaseClientForReadsTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("LETSGO_RELEASE_TOKEN", "from-env")

	client := github.New("workflow-token")
	if got := releaseClientFor(client, "", "workflow-token"); got == client {
		t.Error("LETSGO_RELEASE_TOKEN did not produce a client of its own")
	}
}
