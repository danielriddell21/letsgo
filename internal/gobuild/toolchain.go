package gobuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/safeexec"
)

// ToolchainEnvOverride names an absolute path to a go command, for
// installations the search below cannot find.
const ToolchainEnvOverride = "LETSGO_GO"

// Toolchain resolves the go command to an absolute path.
//
// git and the Go toolchain need opposite treatment, and conflating them breaks
// things. For git, PATH is a threat: its location never varies, so letsgo
// looks only in system directories and never consults PATH at all.
//
// For the Go toolchain, PATH is the selection mechanism. setup-go, gvm, asdf
// and mise all work by putting the chosen version first on PATH, and many
// systems also carry an older Go in /usr/bin. Searching system directories
// first would silently prefer that one — overriding the version the user
// selected, and producing binaries that do not match what anyone else builds.
// That is a worse outcome for a tool whose central claim is reproducibility
// than the risk it would have averted.
//
// So GOROOT first, because it is the toolchain's own declaration of where it
// lives, then PATH. Whatever is found is resolved to an absolute path once and
// invoked directly, and the subprocess is given a fixed PATH regardless (see
// Env), so nothing it execs can be redirected. LETSGO_GO pins the choice
// outright where that matters more than following the user's toolchain
// manager, and the global config's `go` directive pins it machine-wide
// between the two.
//
// It also reports where the choice came from — the env override, the global
// config file, or the fixed GOROOT/PATH search — so plan --explain can say
// why. Nothing is memoized: the caller holds the answer and passes it on.
func Toolchain(global *config.Global) (path, source string, err error) {
	if global == nil {
		global = &config.Global{}
	}
	return resolveToolchainWith(global)
}

func resolveToolchainWith(global *config.Global) (path, source string, err error) {
	name := safeexec.Exe("go")

	if override := os.Getenv(ToolchainEnvOverride); override != "" {
		bin, err := safeexec.Override("go", override, ToolchainEnvOverride)
		if err != nil {
			return "", "", fmt.Errorf("gobuild: %w", err)
		}
		return bin, ToolchainEnvOverride, nil
	}

	if global.Go != "" {
		bin, err := safeexec.Override("go", global.Go, global.Path)
		if err != nil {
			return "", "", fmt.Errorf("gobuild: %w", err)
		}
		return bin, global.Path, nil
	}

	if goroot := os.Getenv("GOROOT"); goroot != "" {
		if candidate := filepath.Join(goroot, "bin", name); safeexec.IsExecutable(candidate) {
			return candidate, "GOROOT", nil
		}
	}

	// Deliberately PATH and not the system directories: see above.
	found, lookErr := exec.LookPath(name)
	if lookErr != nil {
		return "", "", fmt.Errorf(
			"gobuild: the go command was not found\n"+
				"  set %s to its absolute path if it is installed somewhere unusual",
			ToolchainEnvOverride)
	}
	absolute, absErr := filepath.Abs(found)
	if absErr != nil {
		return "", "", fmt.Errorf("gobuild: %w", absErr)
	}
	return absolute, "PATH", nil
}

// Env returns the environment for a go subprocess: the allowlist below, plus
// a PATH containing only system directories and goBin's own directory.
//
// Exported so that everything invoking the toolchain — building, listing
// packages, reading versions — does so under the same conditions.
func Env(t Target, toolchain, goBin string) []string {
	return environ(t, toolchain, goBin)
}

// InstallDir is where a program should be installed to be found on a Go
// developer's PATH: override, then $GOBIN, then $GOPATH/bin. The directory is
// created if it does not exist.
//
// The same place go install puts things, because that is the directory a Go
// developer already has on PATH, and a program they cannot find on PATH is one
// that was not installed, however carefully it was downloaded.
func InstallDir(ctx context.Context, global *config.Global, override string) (string, error) {
	dir := override
	if dir == "" {
		dir = GoEnv(ctx, global, "GOBIN")
	}
	if dir == "" {
		if gopath := GoEnv(ctx, global, "GOPATH"); gopath != "" {
			dir = filepath.Join(gopath, "bin")
		}
	}
	if dir == "" {
		return "", errors.New("gobuild: nowhere to install: set GOBIN or GOPATH, or name a directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("gobuild: %w", err)
	}
	return dir, nil
}

// GoEnv asks the go command when the environment is silent: GOBIN and GOPATH
// have defaults that no environment variable carries, and the answer that
// matters is the one go itself would give.
//
// A silent failure is the right one here. Not knowing a value is not an error;
// it only means the caller has to be told where to put things.
func GoEnv(ctx context.Context, global *config.Global, name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	goBin, _, err := Toolchain(global)
	if err != nil {
		return ""
	}
	out, err := exec.CommandContext(ctx, goBin, "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
