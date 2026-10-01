package lsp

import (
	"fmt"

	"github.com/danielriddell21/letsgo/internal/plugin"
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

// checkPin asks plugin.Resolve, the resolver plugin.Run executes from, and
// words the answer for the editor. It reads files and runs nothing, and keeps
// nothing between calls, so hovering the same line twice asks the disk twice.
//
// dir is the directory letsgo.mod sits in, which a relative command (a
// program inside the repository) is resolved against.
func checkPin(dir string, pin pinLine) pinStatus {
	r := plugin.Resolve(pin.command, pin.digest, dir)
	switch r.State {
	case plugin.Installed:
		if r.Stored {
			return pinStatus{pinInstalled, "installed in the plugin store: " + r.Path}
		}
		return pinStatus{pinInstalled, "on PATH with the pinned digest: " + r.Path}
	case plugin.Missing:
		return pinStatus{pinMissing, "not installed; `letsgo plugin install` fetches the pinned release"}
	case plugin.Mismatch:
		return pinStatus{pinMismatch, fmt.Sprintf("digest mismatch: the pin is %s, but %s is %s",
			plugin.Short(pin.digest), r.Path, plugin.Short(r.Digest))}
	default:
		return pinStatus{pinBroken, r.Err.Error()}
	}
}
