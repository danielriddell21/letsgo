package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/danielriddell21/letsgo/internal/safeexec"
)

// Resolving a helper program through the inherited PATH means whoever controls
// PATH chooses which program runs. A writable directory ahead of /usr/bin — a
// stale ~/bin, a project-local tools directory, an entry added by a shell
// profile — is enough for an attacker-supplied "git" to run with the user's
// privileges, and letsgo runs git on every invocation.
//
// So the search path is fixed here rather than inherited. Only directories
// that require administrative privileges to write are searched, the resolved
// program is invoked by absolute path, and the same fixed PATH is handed to
// the child so that anything git itself execs — credential helpers most
// obviously — is subject to the same restriction.

// gitEnvOverride names an absolute path to a git binary, for installations
// that keep git somewhere other than a system directory. It must be absolute:
// accepting a bare name would reintroduce exactly the lookup this file exists
// to avoid.
const gitEnvOverride = "LETSGO_GIT"

// systemDirs is where git is looked for. See internal/safeexec for why these
// and not, say, /usr/local/bin.
func systemDirs() []string { return safeexec.SystemDirs() }

// fixedPath is systemDirs joined for use as a PATH value.
func fixedPath() string { return safeexec.FixedPath() }

var (
	gitOnce sync.Once
	gitPath string
	gitErr  error
)

// gitBinary resolves git to an absolute path within a system directory. The
// result is cached because it cannot change during a run.
func gitBinary() (string, error) {
	gitOnce.Do(func() {
		gitPath, gitErr = resolveGit(os.Getenv(gitEnvOverride), systemDirs())
	})
	return gitPath, gitErr
}

// resolveGit is the lookup itself, kept separate from the caching so that it
// can be tested with an arbitrary override and search path.
func resolveGit(override string, dirs []string) (string, error) {
	{
		if override != "" {
			if !filepath.IsAbs(override) {
				return "", fmt.Errorf("discover: %s must be an absolute path, got %q",
					gitEnvOverride, override)
			}
			if !isExecutable(override) {
				return "", fmt.Errorf("discover: %s=%q is not an executable file",
					gitEnvOverride, override)
			}
			return override, nil
		}
	}

	if found, err := safeexec.LookIn(dirs, safeexec.Exe("git")); err == nil {
		return found, nil
	}

	// Deliberately no PATH fallback: git is invoked on every run, and a
	// writable directory on PATH would decide which program that is.
	return "", fmt.Errorf(
		"discover: git was not found in any system directory (%s)\n"+
			"  letsgo does not search PATH for git, because a writable directory on PATH\n"+
			"  would let someone else choose which program runs\n"+
			"  if git is installed elsewhere, set %s to its absolute path",
		strings.Join(dirs, ", "), gitEnvOverride)
}

func isExecutable(path string) bool { return safeexec.IsExecutable(path) }

// gitEnv returns the environment for a git subprocess: the caller's, with PATH
// replaced by the fixed one.
//
// The rest is preserved deliberately. HOME in particular decides which
// .gitconfig applies, and dropping it would break repositories that depend on
// safe.directory — which every GitHub Actions checkout does.
func gitEnv() []string { return safeexec.EnvWithFixedPath(os.Environ()) }
