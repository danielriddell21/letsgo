package plan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
)

// tokenNoteFrom describes where a resolved token's source string itself came
// from, for plan --explain: a flag, token-command, or (everything else) an
// environment variable.
func tokenNoteFrom(source string) string {
	switch {
	case strings.HasPrefix(source, "--"):
		return "flag"
	case source == tokenCommandSource:
		return tokenCommandSource
	default:
		return "environment"
	}
}

// TokenEnvVars are the environment variables consulted for a forge token, in
// order. GH_TOKEN is what the GitHub CLI sets, so a machine already set up to
// use gh needs no further configuration.
var TokenEnvVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// TapTokenEnvVars are the environment variables consulted for the token the
// Homebrew tap is written with.
//
// Separate from TokenEnvVars because the two credentials want different
// scopes: the release is published to this repository, which the workflow
// token already covers, and the tap lives in another one, which needs an App
// token. Keeping them apart means the App need not be installed here at all.
var TapTokenEnvVars = []string{"LETSGO_TAP_TOKEN"}

// ReleaseTokenEnvVars are the environment variables consulted for the token
// the GitHub release is published with.
//
// Separate from TokenEnvVars for the same reason TapTokenEnvVars is: a
// repository that wants the release itself attributed to a bot identity,
// distinct from whatever token ran the workflow, needs its own credential to
// name.
var ReleaseTokenEnvVars = []string{"LETSGO_RELEASE_TOKEN"}

// tokenCommandSource is the source Token reports when the value came from
// the global config's token-command, rather than a flag or the environment.
const tokenCommandSource = "token-command"

// Token returns the resolved token, and where it came from.
//
// A nil global is no global config: the flag and the environment still apply.
func Token(ctx context.Context, global *config.Global, override string) (token, source string) {
	if override != "" {
		return override, "--token"
	}
	for _, name := range TokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	if global == nil {
		return "", ""
	}
	if token := runTokenCommand(ctx, global.TokenCommand); token != "" {
		return token, tokenCommandSource
	}
	return "", ""
}

// runTokenCommand runs the global config's token-command and returns its
// output as the token. A missing command, or one that exits non-zero, yields
// no token, with its stderr shown — a broken credential helper must not stop
// the flag/environment fallback chain from reaching "no token" (the PBS edge
// case for token-command).
//
// The token is returned and nothing else: it is never logged, and only the
// argv that produced it is ever recorded, as tokenCommandSource.
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

// TapToken returns the token the Homebrew tap is written with, and where it
// came from.
//
// It falls back to the release token so that a repository which has always
// published with one credential keeps working unchanged. What the fallback
// costs is stated where it is configured, not here: one token that can write
// to both repositories is one token whose loss reaches both.
func TapToken(ctx context.Context, global *config.Global, override, tokenOverride string) (token, source string) {
	if override != "" {
		return override, "--tap-token"
	}
	for _, name := range TapTokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return Token(ctx, global, tokenOverride)
}

// ReleaseToken returns the token the GitHub release is published with, and
// where it came from.
//
// It falls back to the release token so that a repository which has always
// published with one credential keeps working unchanged, exactly as
// TapToken does for the tap.
func ReleaseToken(ctx context.Context, global *config.Global, override, tokenOverride string) (token, source string) {
	if override != "" {
		return override, "--release-token"
	}
	for _, name := range ReleaseTokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return Token(ctx, global, tokenOverride)
}
