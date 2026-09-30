package lsp

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// pinState is whether a pinned plugin can run on this machine.
type pinState int

const (
	pinInstalled pinState = iota // the store, or PATH, holds the pinned binary
	pinMissing                   // nothing by that name anywhere
	pinMismatch                  // a binary on PATH, but not the pinned one
	pinBroken                    // something is there and cannot be read or trusted
)

// pinStatus is a pin's state and a sentence saying why.
type pinStatus struct {
	state  pinState
	detail string
}

// checkPin resolves a pin the way plugin.Run does, in the order `letsgo
// doctor` reports it: the content-addressed store, then PATH re-hashed
// against the pin. It reads files and runs nothing, and keeps nothing between
// calls, so hovering the same line twice asks the disk twice.
//
// dir is the directory letsgo.mod sits in, which a relative command (a
// program inside the repository) is resolved against.
func checkPin(dir string, pin pinLine) pinStatus {
	if store, err := pluginstore.OpenReadOnly(""); err == nil {
		path, ok, err := store.Lookup(pin.digest, pin.command)
		if err != nil {
			return pinStatus{pinBroken, err.Error()}
		}
		if ok {
			return pinStatus{pinInstalled, "installed in the plugin store: " + path}
		}
	}

	path, err := exec.LookPath(commandPath(dir, pin.command))
	if err != nil {
		return pinStatus{pinMissing, "not installed; `letsgo plugin install` fetches the pinned release"}
	}
	digest, err := plugin.DigestOf(path)
	if err != nil {
		return pinStatus{pinBroken, err.Error()}
	}
	if digest != pin.digest {
		return pinStatus{pinMismatch, fmt.Sprintf("digest mismatch: the pin is %s, but %s is %s",
			shortDigest(pin.digest), path, shortDigest(digest))}
	}
	return pinStatus{pinInstalled, "on PATH with the pinned digest: " + path}
}

// commandPath anchors a command written as a relative path to the repository,
// not to wherever the editor happened to start the server.
func commandPath(dir, command string) string {
	if filepath.IsAbs(command) || !strings.ContainsAny(command, `/\`) {
		return command
	}
	return filepath.Join(dir, command)
}

func shortDigest(digest string) string {
	trimmed, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(trimmed) < 12 {
		return digest
	}
	return "sha256:" + trimmed[:12] + "…"
}
