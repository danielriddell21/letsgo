package credential

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

// clearEnv leaves a test no ambient credential, so that a machine with a real
// token in its environment runs the same test as one without.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, names := range [][]string{EnvVars, TapEnvVars, ReleaseEnvVars} {
		for _, name := range names {
			t.Setenv(name, "")
		}
	}
}

func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("token-command runs a shell script, which this test does not build for windows")
	}
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// printArgScript writes its first argument to stdout, so a test can hand the
// exact wanted output as a TokenCommand argument.
func printArgScript(t *testing.T) string {
	t.Helper()
	return script(t, "#!/bin/sh\nprintf '%s' \"$1\"\n")
}

func failingScript(t *testing.T) string {
	t.Helper()
	return script(t, "#!/bin/sh\necho 'no credentials' >&2\nexit 1\n")
}

func TestForgePrecedence(t *testing.T) {
	helper := printArgScript(t)
	global := &config.Global{TokenCommand: []string{helper, "from-command"}}

	tests := []struct {
		name       string
		flag       string
		env        map[string]string
		global     *config.Global
		wantValue  string
		wantSource string
	}{
		{"the flag wins over everything", "from-flag", map[string]string{"GITHUB_TOKEN": "env"}, global, "from-flag", "--token"},
		{"then GITHUB_TOKEN", "", map[string]string{"GITHUB_TOKEN": "gh-env", "GH_TOKEN": "cli-env"}, global, "gh-env", "GITHUB_TOKEN"},
		{"then GH_TOKEN, which the GitHub CLI sets", "", map[string]string{"GH_TOKEN": "cli-env"}, global, "cli-env", "GH_TOKEN"},
		{"then the token-command", "", nil, global, "from-command", CommandSource},
		{"a nil global is no token-command", "", nil, nil, "", ""},
		{"a global without one is no token-command", "", nil, &config.Global{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := Resolve(t.Context(), tt.global, Flags{Token: tt.flag}).Forge
			if got.Value != tt.wantValue || got.Source != tt.wantSource {
				t.Errorf("Forge = %+v, want %q from %q", got, tt.wantValue, tt.wantSource)
			}
		})
	}
}

func TestTapAndReleaseCredentialsHaveTheirOwnFlagAndEnvironmentAndFallBackToTheForge(t *testing.T) {
	tests := []struct {
		name        string
		flags       Flags
		env         map[string]string
		wantTap     Credential
		wantRelease Credential
	}{
		{
			name:        "each own flag",
			flags:       Flags{Token: "f", TapToken: "t-flag", ReleaseToken: "r-flag"},
			env:         map[string]string{"LETSGO_TAP_TOKEN": "t-env", "LETSGO_RELEASE_TOKEN": "r-env"},
			wantTap:     Credential{"t-flag", "--tap-token"},
			wantRelease: Credential{"r-flag", "--release-token"},
		},
		{
			name:        "then each own environment variable",
			flags:       Flags{Token: "f"},
			env:         map[string]string{"LETSGO_TAP_TOKEN": "t-env", "LETSGO_RELEASE_TOKEN": "r-env"},
			wantTap:     Credential{"t-env", "LETSGO_TAP_TOKEN"},
			wantRelease: Credential{"r-env", "LETSGO_RELEASE_TOKEN"},
		},
		{
			// The fallback that keeps every existing repository working.
			name:        "then the forge's flag",
			flags:       Flags{Token: "f"},
			wantTap:     Credential{"f", "--token"},
			wantRelease: Credential{"f", "--token"},
		},
		{
			name:        "and finally the forge's environment",
			env:         map[string]string{"GITHUB_TOKEN": "gh"},
			wantTap:     Credential{"gh", "GITHUB_TOKEN"},
			wantRelease: Credential{"gh", "GITHUB_TOKEN"},
		},
		{
			name:        "one split, one not",
			flags:       Flags{TapToken: "t-flag"},
			env:         map[string]string{"GITHUB_TOKEN": "gh"},
			wantTap:     Credential{"t-flag", "--tap-token"},
			wantRelease: Credential{"gh", "GITHUB_TOKEN"},
		},
		{name: "nothing at all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			set := Resolve(t.Context(), nil, tt.flags)
			if set.Tap != tt.wantTap {
				t.Errorf("Tap = %+v, want %+v", set.Tap, tt.wantTap)
			}
			if set.Release != tt.wantRelease {
				t.Errorf("Release = %+v, want %+v", set.Release, tt.wantRelease)
			}
		})
	}
}

// A token-command that exits non-zero falls through to "no token", not an
// error, with its stderr shown (the PBS edge case).
func TestAFailingTokenCommandYieldsNoToken(t *testing.T) {
	clearEnv(t)
	set := Resolve(t.Context(), &config.Global{TokenCommand: []string{failingScript(t)}}, Flags{})
	if set != (Set{}) {
		t.Errorf("set = %+v, want no credentials", set)
	}
}

// A cancelled run does not wait on a credential helper: the command is not
// started, so there is no token.
func TestAResolveStopsWhenTheRunIsCancelled(t *testing.T) {
	clearEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	set := Resolve(ctx, &config.Global{TokenCommand: []string{printArgScript(t), "helper-token"}}, Flags{})
	if set != (Set{}) {
		t.Errorf("set = %+v, want no credentials", set)
	}
}

func TestTheTokenCommandsOutputIsTrimmed(t *testing.T) {
	clearEnv(t)
	set := Resolve(t.Context(), &config.Global{TokenCommand: []string{printArgScript(t), "helper-token\n"}}, Flags{})
	if set.Forge.Value != "helper-token" {
		t.Errorf("Forge = %q, want no trailing newline", set.Forge.Value)
	}
}

// Three credentials that all fall back to one token-command must not run it
// three times: a helper may prompt, or be rate limited.
func TestTheTokenCommandRunsOnce(t *testing.T) {
	clearEnv(t)
	counter := filepath.Join(t.TempDir(), "runs")
	helper := script(t, "#!/bin/sh\necho x >> \"$1\"\nprintf token\n")

	set := Resolve(t.Context(), &config.Global{TokenCommand: []string{helper, counter}}, Flags{})

	if set.Forge.Value != "token" || set.Tap != set.Forge || set.Release != set.Forge {
		t.Errorf("set = %+v, want all three to carry the one token", set)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if runs := strings.Count(string(data), "x"); runs != 1 {
		t.Errorf("the token-command ran %d times, want 1", runs)
	}
}

// The environment is read for the flag-less credential only when no flag
// names one, so a flag is never shadowed by a stale variable.
func TestAFlagNeverRunsTheTokenCommand(t *testing.T) {
	clearEnv(t)
	counter := filepath.Join(t.TempDir(), "runs")
	helper := script(t, "#!/bin/sh\necho x >> \"$1\"\nprintf token\n")

	Resolve(t.Context(), &config.Global{TokenCommand: []string{helper, counter}}, Flags{Token: "flag"})

	if _, err := os.Stat(counter); err == nil {
		t.Error("the token-command ran although a flag named the token")
	}
}

func TestFrom(t *testing.T) {
	cases := map[string]string{
		"--token":          "flag",
		"--tap-token":      "flag",
		"GITHUB_TOKEN":     "environment",
		"LETSGO_TAP_TOKEN": "environment",
		CommandSource:      CommandSource,
	}
	for source, want := range cases {
		if got := (Credential{"v", source}).From(); got != want {
			t.Errorf("From(%q) = %q, want %q", source, got, want)
		}
	}
}
