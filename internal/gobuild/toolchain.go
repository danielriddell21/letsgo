package gobuild

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/danielriddell21/letsgo/internal/safeexec"
)

// ToolchainEnvOverride names an absolute path to a go command, for
// installations the search below cannot find.
const ToolchainEnvOverride = "LETSGO_GO"

var (
	toolchainOnce sync.Once
	toolchainPath string
	toolchainErr  error
)

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
// manager.
func Toolchain() (string, error) {
	toolchainOnce.Do(func() {
		toolchainPath, toolchainErr = resolveToolchain()
	})
	return toolchainPath, toolchainErr
}

func resolveToolchain() (string, error) {
	name := safeexec.Exe("go")

	if override := os.Getenv(ToolchainEnvOverride); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("gobuild: %s must be an absolute path, got %q",
				ToolchainEnvOverride, override)
		}
		if !safeexec.IsExecutable(override) {
			return "", fmt.Errorf("gobuild: %s=%q is not an executable file",
				ToolchainEnvOverride, override)
		}
		return override, nil
	}

	if goroot := os.Getenv("GOROOT"); goroot != "" {
		if candidate := filepath.Join(goroot, "bin", name); safeexec.IsExecutable(candidate) {
			return candidate, nil
		}
	}

	// Deliberately PATH and not the system directories: see above.
	found, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf(
			"gobuild: the go command was not found\n"+
				"  set %s to its absolute path if it is installed somewhere unusual",
			ToolchainEnvOverride)
	}
	return filepath.Abs(found)
}

// toolchainDir is the directory holding the resolved go command, which the
// subprocess needs on PATH so the toolchain can find its own helpers.
func toolchainDir() string {
	path, err := Toolchain()
	if err != nil {
		return ""
	}
	return filepath.Dir(path)
}

// Env returns the environment for a go subprocess: the allowlist below, plus
// a PATH containing only system directories and the toolchain's own.
//
// Exported so that everything invoking the toolchain — building, listing
// packages, reading versions — does so under the same conditions.
func Env(t Target, toolchain string) []string {
	return environ(t, toolchain)
}
