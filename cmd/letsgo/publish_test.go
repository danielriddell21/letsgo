package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/credential"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/releaser"
)

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
			f := emptyForge(t)
			// A formula needs a macOS or Linux build to install; a Windows
			// host has neither, so it is given one.
			config := "brew you/homebrew-tap\n"
			if runtime.GOOS == "windows" {
				config = "build linux/amd64\n" + config
			}
			t.Chdir(moduleFixtureWith(t, config, nil))

			args := append([]string{"-snapshot", "-o", filepath.Join(t.TempDir(), "dist")}, tc.args...)
			var err error
			out := captureStdout(t, func() { err = f.runRelease(args) })
			if err != nil {
				t.Fatalf("runRelease = %v\n%s", err, out)
			}
			if got := strings.Contains(out, "skipped the Homebrew tap"); got != tc.wantSkipped {
				t.Errorf("tap skipped = %v, want %v\n%s", got, tc.wantSkipped, out)
			}
		})
	}
}

// The forge's client is reused for the tap and the release when they carry the
// same credential. A second client holding the same token would mean a second
// connection pool for no reason, and it would make the single-credential
// arrangement look like a different code path than it is.
func TestClientsReuseTheForgeClientWhenNothingIsSplit(t *testing.T) {
	forgeCred := credential.Credential{Value: "workflow-token", Source: "GITHUB_TOKEN"}
	set := credential.Set{Forge: forgeCred, Tap: forgeCred, Release: forgeCred}

	c := unwired.clients(set)
	if c.Release != forgeRead(c) || c.Tap != forgeRead(c) {
		t.Error("a tap and a release with no credential of their own got second clients")
	}
}

func forgeRead(c releaser.Clients) *github.Client { return c.Read }

// Each of the tap and the release is split on its own: a credential of its own
// gets a client of its own, and the other keeps sharing the forge's.
func TestClientsSplitOnlyWhatHasItsOwnCredential(t *testing.T) {
	forgeCred := credential.Credential{Value: "workflow-token", Source: "GITHUB_TOKEN"}
	tapCred := credential.Credential{Value: "tap-token", Source: "--tap-token"}
	releaseCred := credential.Credential{Value: "release-token", Source: "LETSGO_RELEASE_TOKEN"}

	tests := []struct {
		name     string
		set      credential.Set
		tapSplit bool
		relSplit bool
	}{
		{"tap only", credential.Set{Forge: forgeCred, Tap: tapCred, Release: forgeCred}, true, false},
		{"release only", credential.Set{Forge: forgeCred, Tap: forgeCred, Release: releaseCred}, false, true},
		{"both", credential.Set{Forge: forgeCred, Tap: tapCred, Release: releaseCred}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := unwired.clients(tt.set)
			if got := c.Tap != c.Read; got != tt.tapSplit {
				t.Errorf("tap split = %v, want %v", got, tt.tapSplit)
			}
			if got := c.Release != c.Read; got != tt.relSplit {
				t.Errorf("release split = %v, want %v", got, tt.relSplit)
			}
			// The user agent has to carry over, or a split client's requests
			// arrive unidentified and GitHub is entitled to refuse them.
			for name, client := range map[string]*github.Client{"tap": c.Tap.(*github.Client), "release": c.Release.(*github.Client)} {
				if client.UserAgent != c.Read.UserAgent {
					t.Errorf("%s UserAgent = %q, want %q", name, client.UserAgent, c.Read.UserAgent)
				}
			}
		})
	}
}

// credentials reads the environment when the flag is absent, which is how CI
// passes the tap and release tokens.
func TestCredentialsReadTheEnvironmentWhenNoFlagIsGiven(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "workflow-token")
	t.Setenv("LETSGO_TAP_TOKEN", "tap-from-env")
	t.Setenv("LETSGO_RELEASE_TOKEN", "release-from-env")

	c := unwired.clients(credentials(t.Context(), credential.Flags{}))
	if c.Tap == c.Read || c.Release == c.Read {
		t.Error("LETSGO_TAP_TOKEN and LETSGO_RELEASE_TOKEN did not produce clients of their own")
	}
}
