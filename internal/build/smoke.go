package build

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Smoke describes a check that runs a freshly built binary.
type Smoke struct {
	// Args are passed to the binary. Defaults to --version.
	Args []string

	// Want must appear in the output.
	Want string

	// Timeout bounds the run. Defaults to ten seconds.
	Timeout time.Duration
}

// crashMarkers appear in the output of a Go program that died rather than
// exited. They are how a crash is told apart from an ordinary non-zero exit,
// because the status code cannot do it: flag parsing and a panic both exit 2.
var crashMarkers = []string{"panic: ", "fatal error: ", "runtime error: ", "signal SIGSEGV"}

// runSmoke executes a built binary and checks its output.
//
// Verifying that a -X target symbol exists is a structural check: it proves
// the linker had somewhere to write. Running the binary is an empirical one,
// and it catches what the structural check cannot — an initialiser that
// overwrites the value, a version string assembled wrongly, or a binary that
// panics before reaching main.
//
// The tricky case is a binary with no --version flag, which is a perfectly
// ordinary thing to ship. Its non-zero exit looks exactly like a crash, so
// failing on exit status alone would block releases for a design choice rather
// than a defect. Instead a crash is identified by what a dying Go program
// prints, and anything else that exits non-zero is reported as a warning: the
// version could not be confirmed, which is worth saying and not worth
// stopping for.
//
// It returns a warning string when the check could not be performed, and an
// error only when the binary is demonstrably broken.
func runSmoke(ctx context.Context, binary string, s Smoke) (warning string, err error) {
	args := s.Args
	if len(args) == 0 {
		args = []string{"--version"}
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)

	// Cancelling the context kills the process letsgo started, but not any
	// process that one started in turn. A grandchild inherits the output pipe,
	// and CombinedOutput waits for every writer to close it — so without this
	// the timeout bounds the process and not the call, and a binary that
	// spawns anything long-lived hangs the release instead of failing it.
	//
	// WaitDelay caps how long Wait will block on I/O after the context is
	// done, then closes the pipes itself.
	cmd.WaitDelay = 2 * time.Second

	// Deliberately not cmd.Output: a program that prints its version to
	// stderr is unusual but not wrong, and failing it would be a false alarm.
	out, runErr := cmd.CombinedOutput()
	printed := strings.TrimSpace(string(out))

	invocation := filepath.Base(binary) + " " + strings.Join(args, " ")

	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("build: smoke test: %s did not exit within %s", invocation, timeout)
	}

	if runErr != nil {
		for _, marker := range crashMarkers {
			if strings.Contains(printed, marker) {
				return "", fmt.Errorf("build: smoke test: %s crashed\n  output: %s", invocation, printed)
			}
		}
		// Not a crash, so most likely a binary that does not take this flag.
		return fmt.Sprintf("%s exited %s, so the version could not be confirmed", invocation, exitStatus(runErr)), nil
	}

	if s.Want != "" && !strings.Contains(printed, s.Want) {
		return "", fmt.Errorf(
			"build: smoke test: %s printed %q, which does not contain %q\n"+
				"  the linker wrote the version but the binary does not report it;\n"+
				"  a variable initialised at run time would overwrite it",
			invocation, printed, s.Want)
	}
	return "", nil
}

func exitStatus(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("with status %d", exit.ExitCode())
	}
	return "unsuccessfully"
}
