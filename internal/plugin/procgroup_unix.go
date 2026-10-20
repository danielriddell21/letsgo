//go:build unix

package plugin

import (
	"os/exec"
	"syscall"
)

// inOwnGroup starts cmd as the leader of a new process group, and makes
// cancelling it signal the whole group, so whatever the plugin started stops
// with it.
func inOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// The negative pid names the group the plugin leads.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
