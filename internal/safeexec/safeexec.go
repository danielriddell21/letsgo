// Package safeexec constrains where helper programs are found and what they
// can find in turn.
//
// Resolving a program through the inherited PATH means whoever controls PATH
// chooses which program runs. A writable directory ahead of /usr/bin — a stale
// ~/bin, a project-local tools directory, an entry added by a shell profile —
// is enough for an attacker-supplied binary to run with the user's privileges.
//
// The same applies to the child: a subprocess given the inherited PATH can be
// redirected in exactly the same way when it execs something of its own.
package safeexec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SystemDirs lists directories an ordinary user cannot write to.
//
// Deliberately excludes /usr/local/bin and /opt/homebrew/bin. Both are the
// normal home of user-installed software and are routinely owned by the
// logged-in user, which is precisely the property that disqualifies them.
func SystemDirs() []string { return systemDirsFor(runtime.GOOS, os.Getenv) }

// systemDirsFor is SystemDirs for a named platform and environment, so the
// Windows branch can be tested wherever the tests run.
func systemDirsFor(goos string, getenv func(string) string) []string {
	if goos == "windows" {
		root := getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		dirs := []string{filepath.Join(root, "system32"), root}
		// Git for Windows installs under Program Files, which is writable
		// only with elevation.
		for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
			if base := getenv(key); base != "" {
				dirs = append(dirs,
					filepath.Join(base, "Git", "cmd"),
					filepath.Join(base, "Git", "bin"))
			}
		}
		return dirs
	}
	return []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}
}

// FixedPath is SystemDirs joined for use as a PATH value, plus any extra
// directories the caller must include.
//
// Extras exist for programs whose location is legitimately variable — the Go
// toolchain is installed wherever a version manager put it — and are appended
// after the system directories so they cannot shadow them.
func FixedPath(extra ...string) string {
	dirs := SystemDirs()
	for _, dir := range extra {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// EnvWithFixedPath returns env with every PATH entry replaced by a fixed one.
func EnvWithFixedPath(env []string, extra ...string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		// Windows environment variable names are case-insensitive, so "Path"
		// and "PATH" must both go or one would shadow the replacement.
		if !ok || strings.EqualFold(key, "PATH") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "PATH="+FixedPath(extra...))
}

// IsExecutable reports whether path is a regular file that can be run.
func IsExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return runnable(runtime.GOOS, info.Mode())
}

// runnable is whether a file of this mode can be run on goos. Windows has no
// execute bit, so any regular file counts there.
func runnable(goos string, mode fs.FileMode) bool {
	if !mode.IsRegular() {
		return false
	}
	return goos == "windows" || mode.Perm()&0o111 != 0
}

// Override validates a path the user named outright for a program, instead of
// one letsgo would look for: it must be absolute and an executable file. name
// is the program ("git", "tool gofumpt") and origin says where the path came
// from (an environment variable, or a config file's path), so the error can
// tell the user what to fix.
//
// This is the one check behind every such override, because it is security
// sensitive: accepting a bare name would reintroduce exactly the PATH lookup
// this package exists to avoid. Each caller keeps its own fallback order for
// when there is no override.
func Override(name, value, origin string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s %q from %s must be an absolute path", name, value, origin)
	}
	if !IsExecutable(value) {
		return "", fmt.Errorf("%s %q from %s is not an executable file", name, value, origin)
	}
	return value, nil
}

// LookIn finds the first of names present in dirs, returning an absolute path.
func LookIn(dirs []string, names ...string) (string, error) {
	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if IsExecutable(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("safeexec: %s not found in %s",
		strings.Join(names, " or "), strings.Join(dirs, ", "))
}

// Exe appends the platform's executable suffix.
func Exe(name string) string { return exeFor(runtime.GOOS, name) }

func exeFor(goos, name string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
