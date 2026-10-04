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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/pluginstore"
	pub "github.com/danielriddell21/letsgo/plugin"
)

// Hook is a point in the release a plugin can answer for. The type, its
// constants and the closed set of hooks live in the public
// github.com/danielriddell21/letsgo/plugin package; see hooks.go for why.
type Hook = pub.Hook

const (
	HookLDFlags       = pub.HookLDFlags
	HookArchiveLayout = pub.HookArchiveLayout
	HookTapFiles      = pub.HookTapFiles
)

// Hooks is the closed set, in the order they run.
var Hooks = pub.Hooks

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

// Run executes the plugin in dir, sending input as JSON on stdin and decoding
// its stdout into output.
//
// The executable is hashed and compared against the pin before it runs.
// Checking afterwards would be checking what we already executed.
//
// pluginsDir is the global config's `plugins` directive, or empty: where the
// plugin store lives when nothing overrides it.
//
// dir is the repository root, and input's ConfigDir field (present on every
// hook) names where a plugin's own config lives: dir + "/.letsgo". A plugin
// needing settings of its own reads a file there rather than guessing, which
// is how letsgo's own config stays a closed set while a plugin still takes
// settings. A legacy root-relative file beside letsgo.mod is still read by
// plugins that have not moved yet, with a plan Warn suggesting they do.
func Run(ctx context.Context, p Plugin, dir, pluginsDir string, input, output any) error {
	path, err := resolve(p, dir, pluginsDir)
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
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(payload)

	// The caller's environment, whole. Trimming it would be theatre: a plugin
	// can read files, the clock and the network, so what makes its answer safe
	// is that the answer is recorded and replayed, not that its inputs were
	// rationed. One hook exists precisely to read the environment.
	//
	// tap-files is the one exception: it renders files for core to write, and
	// core is the one that talks to the forge and the tap. A credential that
	// can publish to someone else's repository has no reason to be in this
	// process's environment at all.
	cmd.Env = os.Environ()
	if p.Hook == HookTapFiles {
		cmd.Env = withoutEnv(cmd.Env, "GITHUB_TOKEN", "GH_TOKEN", "LETSGO_TAP_TOKEN", "LETSGO_RELEASE_TOKEN")
	}

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

// withoutEnv returns env with any variable named in drop removed.
func withoutEnv(env []string, drop ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(drop, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// resolve is Resolve, as the error Run refuses with.
func resolve(p Plugin, dir, pluginsDir string) (string, error) {
	if p.Hook == "" || !p.Hook.Valid() {
		return "", fmt.Errorf("plugin %s: %q is not a hook letsgo knows", p.Command, p.Hook)
	}

	r := Resolve(p.Command, p.Digest, dir, pluginsDir)
	switch r.State {
	case Installed:
		return r.Path, nil
	case Mismatch:
		return "", r.mismatch(p.Command, p.Digest)
	case Missing:
		return "", fmt.Errorf("plugin %s: not installed: %w", p.Command, r.Err)
	default:
		return "", fmt.Errorf("plugin %s: %w", p.Command, r.Err)
	}
}

// DigestOf is the SHA-256 of the file at path, as "sha256:…".
//
// Exported so that anything reporting on a pin computes the digest the same
// way resolve does. Two implementations of this would be two answers to the
// question the pin exists to settle.
func DigestOf(path string) (string, error) { return pluginstore.DigestOf(path) }

func short(digest string) string {
	if trimmed, ok := strings.CutPrefix(digest, "sha256:"); ok && len(trimmed) >= 12 {
		return trimmed[:12]
	}
	return digest
}
