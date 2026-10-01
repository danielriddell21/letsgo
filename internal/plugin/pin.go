package plugin

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// State is whether a pinned plugin can run on this machine.
type State int

const (
	Installed State = iota // the store, or PATH, holds the pinned binary
	Missing                // nothing by that name anywhere
	Mismatch               // a binary on PATH, but not the pinned one
	Broken                 // something is there and cannot be read or trusted
)

// Resolution is the answer to "can this pin run here?".
type Resolution struct {
	State State

	// Path is the executable found: the one to run when Installed, the one
	// that failed the check when Mismatch.
	Path string

	// Digest is what Path actually hashes to, set for Mismatch.
	Digest string

	// Err says why, set for Missing and Broken.
	Err error
}

// Resolve finds the executable a pin names and proves it is the one that was
// pinned. It is the one answer to that question: Run executes what it returns,
// and doctor, `plugin list`, `plugin install` and the editor report it.
//
// The store is checked first, because that is where `plugin install` puts
// things and it needs no PATH entry to find. A store entry that exists but no
// longer hashes to its own path is Broken rather than a fall-through to PATH,
// which would turn a tampered store into a silent substitution. Nothing in the
// store stays trusted just for having been found there, either: PATH is
// re-hashed against the pin.
//
// dir is the repository root. A command written as a relative path (a program
// inside the repository) is anchored to it once, so the file that is hashed is
// the file that runs, wherever the process was started. Resolve reads files
// and runs nothing, and never creates the store.
func Resolve(command, digest, dir string) Resolution {
	if store, err := pluginstore.OpenReadOnly(""); err == nil {
		path, ok, err := store.Lookup(digest, command)
		if err != nil {
			return Resolution{State: Broken, Err: err}
		}
		if ok {
			return Resolution{State: Installed, Path: path}
		}
	}

	path, err := exec.LookPath(anchor(dir, command))
	if err != nil {
		return Resolution{State: Missing, Err: err}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}

	got, err := DigestOf(path)
	if err != nil {
		return Resolution{State: Broken, Path: path, Err: err}
	}
	if got != digest {
		return Resolution{State: Mismatch, Path: path, Digest: got}
	}
	return Resolution{State: Installed, Path: path}
}

// anchor makes a command written as a relative path relative to dir. A bare
// name is left for PATH.
func anchor(dir, command string) string {
	if filepath.IsAbs(command) || !strings.ContainsAny(command, `/\`) {
		return command
	}
	return filepath.Join(dir, command)
}

// Short abbreviates a "sha256:…" digest for a one-line report.
func Short(digest string) string {
	if trimmed, ok := strings.CutPrefix(digest, "sha256:"); ok && len(trimmed) >= 12 {
		return "sha256:" + trimmed[:12] + "…"
	}
	return digest
}

// mismatch is Run's refusal to execute something that is not the pin.
func (r Resolution) mismatch(command, digest string) error {
	return fmt.Errorf(
		"plugin %s: %s is %s, but the config pins %s;\n"+
			"install the pinned version or update the pin — a plugin decides what gets built,\n"+
			"so running a different one would produce a release nobody can account for",
		command, r.Path, short(r.Digest), short(digest))
}
