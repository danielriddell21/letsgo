package plan

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

// CD-12: token-command is the last fallback tier, tried only once a flag and
// the environment have both come up empty — which is exactly what tokenWith
// covers, since Token only reaches it after checking both.
func TestTokenWithRunsTheGlobalTokenCommand(t *testing.T) {
	helper := printArgScript(t)

	got, source := tokenWith(&config.Global{TokenCommand: []string{helper, "helper-token"}})
	if got != "helper-token" || source != tokenCommandSource {
		t.Errorf("tokenWith() = %q, %q; want %q, %q", got, source, "helper-token", tokenCommandSource)
	}
}

func TestTokenWithTrimsTrailingWhitespace(t *testing.T) {
	helper := printArgScript(t)

	got, _ := tokenWith(&config.Global{TokenCommand: []string{helper, "helper-token\n"}})
	if got != "helper-token" {
		t.Errorf("tokenWith() token = %q, want no trailing newline", got)
	}
}

// The PBS edge case: a token-command that exits non-zero falls through to
// "no token", not an error.
func TestTokenWithFallsThroughOnANonZeroExit(t *testing.T) {
	helper := failingScript(t)

	got, source := tokenWith(&config.Global{TokenCommand: []string{helper}})
	if got != "" || source != "" {
		t.Errorf("tokenWith() = %q, %q; want no token", got, source)
	}
}

func TestTokenWithWithoutATokenCommandYieldsNoToken(t *testing.T) {
	got, source := tokenWith(&config.Global{})
	if got != "" || source != "" {
		t.Errorf("tokenWith() = %q, %q; want no token", got, source)
	}
}

func TestTokenNoteFrom(t *testing.T) {
	cases := map[string]string{
		"--token":          "flag",
		"GITHUB_TOKEN":     "environment",
		tokenCommandSource: tokenCommandSource,
	}
	for source, want := range cases {
		if got := tokenNoteFrom(source); got != want {
			t.Errorf("tokenNoteFrom(%q) = %q, want %q", source, got, want)
		}
	}
}

// printArgScript writes its first argument to stdout, so a test can hand the
// exact wanted output as a TokenCommand argument rather than baking it into
// the script's own source.
func printArgScript(t *testing.T) string {
	t.Helper()
	return script(t, "#!/bin/sh\nprintf '%s' \"$1\"\n")
}

func failingScript(t *testing.T) string {
	t.Helper()
	return script(t, "#!/bin/sh\necho 'no credentials' >&2\nexit 1\n")
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
