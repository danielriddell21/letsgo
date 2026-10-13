//go:build !unix

package plugin

import "os/exec"

// inOwnGroup leaves cmd as it is: there is no process group to start it in,
// so cancelling it kills the plugin alone, and WaitDelay bounds the wait for
// anything it left behind.
func inOwnGroup(*exec.Cmd) {}
