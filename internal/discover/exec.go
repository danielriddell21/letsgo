package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// systemDirs lists directories an ordinary user cannot write to.
//
// Deliberately excludes /usr/local/bin and /opt/homebrew/bin. Both are the
// normal home of user-installed software and are routinely owned by the
// logged-in user, which is precisely the property that disqualifies them.
func systemDirs() []string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		dirs := []string{
			filepath.Join(root, "system32"),
			root,
		}
		// Git for Windows installs under Program Files, which is writable only
		// with elevation.
		for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
			if base := os.Getenv(key); base != "" {
				dirs = append(dirs,
					filepath.Join(base, "Git", "cmd"),
					filepath.Join(base, "Git", "bin"),
				)
			}
		}
		return dirs
	}

	return []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}
}

// fixedPath is systemDirs joined for use as a PATH value.
func fixedPath() string {
	return strings.Join(systemDirs(), string(os.PathListSeparator))
}

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

	names := []string{"git"}
	if runtime.GOOS == "windows" {
		names = []string{"git.exe"}
	}

	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if isExecutable(candidate) {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf(
		"discover: git was not found in any system directory (%s)\n"+
			"  letsgo does not search PATH for git, because a writable directory on PATH\n"+
			"  would let someone else choose which program runs\n"+
			"  if git is installed elsewhere, set %s to its absolute path",
		strings.Join(dirs, ", "), gitEnvOverride)
}

// isExecutable reports whether path is a regular file that can be run.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// gitEnv returns the environment for a git subprocess: the caller's, with PATH
// replaced by the fixed one.
//
// The rest is preserved deliberately. HOME in particular decides which
// .gitconfig applies, and dropping it would break repositories that depend on
// safe.directory — which every GitHub Actions checkout does.
func gitEnv() []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent)+1)

	for _, entry := range parent {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		// Windows environment variable names are case-insensitive, so "Path"
		// and "PATH" must both be replaced rather than one shadowing the other.
		if strings.EqualFold(key, "PATH") {
			continue
		}
		out = append(out, entry)
	}

	return append(out, "PATH="+fixedPath())
}
