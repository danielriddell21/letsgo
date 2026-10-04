// Package credential resolves the tokens a release is written with.
//
// A token comes from a command-line flag, then the environment, then the
// global config's token-command. This package owns that lookup so that a
// command resolves its credentials once, at the composition root, and passes
// the result down (ADR-0022), instead of every consumer importing the release
// planner to read an environment variable. It is a leaf: it imports only
// config, for the token-command.
//
// Three credentials exist because the three things a release writes to want
// different scopes: the repository's own release, the Homebrew tap that lives
// in another repository, and the release itself under a bot identity.
// Resolve returns all three, each carrying where its value came from.
package credential

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
)

// EnvVars are the environment variables consulted for a forge token, in
// order. GH_TOKEN is what the GitHub CLI sets, so a machine already set up to
// use gh needs no further configuration.
var EnvVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// TapEnvVars are the environment variables consulted for the token the
// Homebrew tap is written with.
//
// Separate from EnvVars because the two credentials want different scopes:
// the release is published to this repository, which the workflow token
// already covers, and the tap lives in another one, which needs an App token.
// Keeping them apart means the App need not be installed here at all.
var TapEnvVars = []string{"LETSGO_TAP_TOKEN"}

// ReleaseEnvVars are the environment variables consulted for the token the
// GitHub release is published with.
//
// Separate from EnvVars for the same reason TapEnvVars is: a repository that
// wants the release itself attributed to a bot identity, distinct from
// whatever token ran the workflow, needs its own credential to name.
var ReleaseEnvVars = []string{"LETSGO_RELEASE_TOKEN"}

// Flag names, which are also the Source of a credential given on the command
// line.
const (
	flagToken        = "--token"
	flagTapToken     = "--tap-token"
	flagReleaseToken = "--release-token"
)

// CommandSource is the Source of a credential that came from the global
// config's token-command, rather than a flag or the environment.
const CommandSource = "token-command"

// Flags are the credentials given on the command line. Each is empty when its
// flag was not.
type Flags struct {
	Token, TapToken, ReleaseToken string
}

// Credential is a token and where it came from. The zero value is no token.
type Credential struct {
	Value string

	// Source names where Value was found: a flag, an environment variable, or
	// CommandSource. Empty when there is no Value. It never contains the
	// token itself.
	Source string
}

// Set is the three credentials of a release.
type Set struct {
	// Forge is the credential for the repository's own forge.
	Forge Credential

	// Tap is the credential the Homebrew tap is written with, and Release the
	// one the GitHub release is published with. Each falls back to Forge so
	// that a repository which has always published with one credential keeps
	// working unchanged. What the fallback costs is stated where it is
	// configured, not here: one token that can write to both repositories is
	// one token whose loss reaches both.
	Tap     Credential
	Release Credential
}

// From says what kind of place a credential's Source is, for `plan
// --explain`: a flag, the token-command, or (everything else) an environment
// variable.
func (c Credential) From() string {
	switch {
	case strings.HasPrefix(c.Source, "--"):
		return "flag"
	case c.Source == CommandSource:
		return CommandSource
	default:
		return "environment"
	}
}

// Resolve finds the three credentials. A nil global is no global config: the
// flags and the environment still apply.
//
// The token-command runs at most once, however many of the credentials fall
// back to the forge's.
func Resolve(ctx context.Context, global *config.Global, f Flags) Set {
	forge := forgeCredential(ctx, global, f.Token)
	return Set{
		Forge:   forge,
		Tap:     own(f.TapToken, flagTapToken, TapEnvVars, forge),
		Release: own(f.ReleaseToken, flagReleaseToken, ReleaseEnvVars, forge),
	}
}

func forgeCredential(ctx context.Context, global *config.Global, flag string) Credential {
	if flag != "" {
		return Credential{flag, flagToken}
	}
	if c := fromEnv(EnvVars); c.Value != "" {
		return c
	}
	if global == nil {
		return Credential{}
	}
	if token := runTokenCommand(ctx, global.TokenCommand); token != "" {
		return Credential{token, CommandSource}
	}
	return Credential{}
}

// own resolves a credential that has a flag and environment variables of its
// own, and falls back to fallback when neither is set.
func own(flag, flagName string, envVars []string, fallback Credential) Credential {
	if flag != "" {
		return Credential{flag, flagName}
	}
	if c := fromEnv(envVars); c.Value != "" {
		return c
	}
	return fallback
}

func fromEnv(names []string) Credential {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return Credential{v, name}
		}
	}
	return Credential{}
}

// runTokenCommand runs the global config's token-command and returns its
// output as the token. A missing command, or one that exits non-zero, yields
// no token, with its stderr shown: a broken credential helper must not stop
// the flag/environment fallback chain from reaching "no token" (the PBS edge
// case for token-command).
//
// The token is returned and nothing else: it is never logged, and only the
// argv that produced it is ever recorded, as CommandSource.
func runTokenCommand(ctx context.Context, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			fmt.Fprintf(os.Stderr, "token-command: %v: %s\n", err, detail)
		} else {
			fmt.Fprintf(os.Stderr, "token-command: %v\n", err)
		}
		return ""
	}
	return strings.TrimSpace(string(out))
}
