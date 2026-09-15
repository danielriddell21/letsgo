// Package plugin runs the external programs a repository may put in the middle
// of a release.
//
// One rule governs everything here: a plugin may not change the released bytes
// unless its output is recorded. Core invokes a plugin, writes what came back
// into the manifest, and verification replays that record — so `letsgo verify`
// never runs a plugin, and a release stays reproducible on a machine that has
// none of them installed.
//
// Two consequences follow, and both are deliberate. The set of hooks is closed:
// a plugin answers a question core already asks itself, rather than reaching
// into the build. And every plugin is pinned by digest, because a program that
// decides what gets compiled is a build input exactly as the compiler is.
package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Hook is a point in the release a plugin can answer for.
type Hook string

const (
	// HookLDFlags asks for extra -X assignments. The answer is recorded in the
	// artifact's ldflags, which verification already replays exactly.
	HookLDFlags Hook = "ldflags"

	// HookArchiveLayout asks which binaries share an archive. The answer is
	// recorded as the artifact's binaries.
	HookArchiveLayout Hook = "archive-layout"
)

// Hooks is the closed set, in the order they run.
var Hooks = []Hook{HookLDFlags, HookArchiveLayout}

// Valid reports whether a hook is one letsgo knows.
func (h Hook) Valid() bool {
	for _, known := range Hooks {
		if h == known {
			return true
		}
	}
	return false
}

// Plugin is one configured plugin, pinned to the exact program that ran.
type Plugin struct {
	Hook Hook

	// Command is the executable's name, looked up on PATH, or a path to it.
	Command string

	// Version is what the repository asked for. Recorded for a reader; the
	// digest is what is actually checked.
	Version string

	// Digest is the SHA-256 of the executable, as "sha256:…". A plugin decides
	// what gets built, so an unpinned one would be an unrecorded build input.
	Digest string
}

// timeout bounds a plugin's run. A hook answers a question from data it is
// handed; one that has not answered in a minute is stuck, and a release that
// hangs is worse than one that fails.
const timeout = time.Minute

// Run executes the plugin, sending input as JSON on stdin and decoding its
// stdout into output.
//
// The executable is hashed and compared against the pin before it runs.
// Checking afterwards would be checking what we already executed.
func Run(ctx context.Context, p Plugin, input, output any) error {
	path, err := resolve(p)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("plugin %s: encoding input: %w", p.Command, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, string(p.Hook))
	cmd.Stdin = bytes.NewReader(payload)

	// A plugin's environment is the caller's minus anything that would let it
	// reach back into the build: it is answering a question, not compiling.
	cmd.Env = environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("plugin %s: %w\n%s", p.Command, err, detail)
		}
		return fmt.Errorf("plugin %s: %w", p.Command, err)
	}

	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("plugin %s: reading its answer: %w", p.Command, err)
	}
	return nil
}

// resolve finds the executable and proves it is the one that was pinned.
func resolve(p Plugin) (string, error) {
	if p.Hook == "" || !p.Hook.Valid() {
		return "", fmt.Errorf("plugin %s: %q is not a hook letsgo knows", p.Command, p.Hook)
	}

	path, err := exec.LookPath(p.Command)
	if err != nil {
		return "", fmt.Errorf("plugin %s: not installed: %w", p.Command, err)
	}

	digest, err := digestOf(path)
	if err != nil {
		return "", err
	}
	if digest != p.Digest {
		return "", fmt.Errorf(
			"plugin %s: %s is %s, but the config pins %s;\n"+
				"install the pinned version or update the pin — a plugin decides what gets built,\n"+
				"so running a different one would produce a release nobody can account for",
			p.Command, path, short(digest), short(p.Digest))
	}
	return path, nil
}

func digestOf(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("plugin: reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", fmt.Errorf("plugin: reading %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil)), nil
}

// passthrough names what a plugin genuinely needs: where to find itself and
// somewhere to write scratch files. Everything else is dropped, so that a
// plugin's answer depends on its input rather than on the shell it inherited.
var passthrough = []string{
	"PATH", "HOME",
	"TMPDIR", "TMP", "TEMP",
	"USERPROFILE", "LOCALAPPDATA", "APPDATA",
	"SystemRoot", "windir", "ComSpec", "PATHEXT",
}

func environ() []string {
	out := make([]string, 0, len(passthrough)+1)
	for _, key := range passthrough {
		if v, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+v)
		}
	}
	return append(out, "LC_ALL=C")
}

func short(digest string) string {
	if trimmed, ok := strings.CutPrefix(digest, "sha256:"); ok && len(trimmed) >= 12 {
		return trimmed[:12]
	}
	return digest
}
