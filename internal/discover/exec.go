package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
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

// GitBinary resolves git for this run: the env override, then the global
// config's git, then a system directory. It reports where the choice came
// from so plan --explain can say why.
//
// Nothing is cached: the caller resolves once at the composition root and
// passes the path on, so two runs in one process can use different settings.
// A nil global is the same as no config file.
func GitBinary(global *config.Global) (path, source string, err error) {
	if global == nil {
		global = &config.Global{}
	}
	return resolveGit(os.Getenv(gitEnvOverride), global, systemDirs())
}

// resolveGit is the lookup itself, kept separate from the environment so that it
// can be tested with an arbitrary override, global config and search path.
func resolveGit(override string, global *config.Global, dirs []string) (path, source string, err error) {
	if override != "" {
		if !filepath.IsAbs(override) {
			return "", "", fmt.Errorf("discover: %s must be an absolute path, got %q",
				gitEnvOverride, override)
		}
		if !isExecutable(override) {
			return "", "", fmt.Errorf("discover: %s=%q is not an executable file",
				gitEnvOverride, override)
		}
		return override, gitEnvOverride, nil
	}

	if global.Git != "" {
		if !filepath.IsAbs(global.Git) {
			return "", "", fmt.Errorf("discover: git %q in %s must be an absolute path", global.Git, global.Path)
		}
		if !isExecutable(global.Git) {
			return "", "", fmt.Errorf("discover: git %q in %s is not an executable file", global.Git, global.Path)
		}
		return global.Git, global.Path, nil
	}

	if found, lookErr := safeexec.LookIn(dirs, safeexec.Exe("git")); lookErr == nil {
		return found, "a system directory", nil
	}

	// Deliberately no PATH fallback: git is invoked on every run, and a
	// writable directory on PATH would decide which program that is.
	return "", "", fmt.Errorf(
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
