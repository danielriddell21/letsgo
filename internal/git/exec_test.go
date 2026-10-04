package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/safeexec"
)

// testGit resolves git the way the composition root does, without a global
// config.
func testGit(t *testing.T) string {
	t.Helper()
	bin, _, err := Binary(nil)
	if err != nil {
		t.Fatalf("Binary: %v", err)
	}
	return bin
}

func TestBinary(t *testing.T) {
	bin, source, err := Binary(&config.Global{})
	if err != nil {
		t.Fatalf("Binary: %v", err)
	}
	if source == "" {
		t.Error("Binary reported no source")
	}
	if !filepath.IsAbs(bin) {
		t.Errorf("git resolved to %q, want an absolute path", bin)
	}

	dir := filepath.Dir(bin)
	for _, allowed := range safeexec.SystemDirs() {
		if dir == allowed {
			return
		}
	}
	t.Errorf("git resolved to %q, which is outside the permitted directories %v", bin, safeexec.SystemDirs())
}

// The directories most likely to be user-owned are the ones most worth
// excluding, and they are also the ones most likely to be added back by
// someone who has not read this file.
func TestSystemDirsExcludeUserWritableLocations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	forbidden := []string{"/usr/local/bin", "/usr/local/sbin", "/opt/homebrew/bin", ".", ""}
	for _, dir := range safeexec.SystemDirs() {
		for _, bad := range forbidden {
			if dir == bad {
				t.Errorf("%q is writable without elevation and must not be searched", dir)
			}
		}
		if !filepath.IsAbs(dir) {
			t.Errorf("%q is not absolute", dir)
		}
	}
}

// The whole point of the fixed search path is that PATH cannot redirect us.
// This plants a hostile git early on PATH and checks it is never run.
func TestPoisonedPathIsIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}

	evil := t.TempDir()
	marker := filepath.Join(evil, "executed")
	script := "#!/bin/sh\ntouch " + marker + "\necho poisoned\nexit 0\n"
	if err := os.WriteFile(filepath.Join(evil, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()

	// PATH now contains nothing but the attacker's directory.
	t.Setenv("PATH", evil)

	ctx := context.Background()
	initRepo(t, repo)

	g, err := runner(testGit(t), repo).State(ctx)
	if err != nil {
		t.Fatalf("FindGit with a poisoned PATH: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the git binary on PATH was executed")
	}
	if len(g.Commit) != 40 {
		t.Errorf("Commit = %q, want a real sha from the system git", g.Commit)
	}
}

// A child process must not be handed the PATH we refused to use ourselves:
// git resolves credential helpers through it.
func TestGitEnvReplacesPath(t *testing.T) {
	t.Setenv("PATH", "/tmp/attacker-controlled")

	var paths []string
	for _, entry := range gitEnv() {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, "PATH") {
			paths = append(paths, value)
		}
	}

	if len(paths) != 1 {
		t.Fatalf("got %d PATH entries, want exactly 1: %v", len(paths), paths)
	}
	if paths[0] != safeexec.FixedPath() {
		t.Errorf("PATH = %q, want %q", paths[0], safeexec.FixedPath())
	}
	if strings.Contains(paths[0], "attacker-controlled") {
		t.Error("the inherited PATH survived into the child environment")
	}
}

// HOME decides which .gitconfig applies. Dropping it would break every
// repository relying on safe.directory, which includes all GitHub Actions
// checkouts.
func TestGitEnvPreservesHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not the relevant variable on Windows")
	}
	t.Setenv("HOME", "/home/someone")

	for _, entry := range gitEnv() {
		if entry == "HOME=/home/someone" {
			return
		}
	}
	t.Error("HOME did not survive into the git environment")
}

func TestGitOverrideMustBeAbsoluteAndExecutable(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "git")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notExecutable := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, _, err := resolveGit(good, &config.Global{}, nil); err != nil || got != good {
		t.Errorf("resolveGit(absolute executable) = %q, %v", got, err)
	}

	// A bare name would be resolved through PATH by the operating system,
	// reintroducing the exact problem this code exists to prevent.
	if _, _, err := resolveGit("git", &config.Global{}, nil); err == nil {
		t.Error("resolveGit accepted a bare name, want an error")
	}
	if _, _, err := resolveGit("./git", &config.Global{}, nil); err == nil {
		t.Error("resolveGit accepted a relative path, want an error")
	}
	if runtime.GOOS != "windows" {
		if _, _, err := resolveGit(notExecutable, &config.Global{}, nil); err == nil {
			t.Error("resolveGit accepted a non-executable file, want an error")
		}
	}
}

func TestResolveGitReportsAbsenceUsefully(t *testing.T) {
	_, _, err := resolveGit("", &config.Global{}, []string{filepath.Join(t.TempDir(), "nowhere")})
	if err == nil {
		t.Fatal("resolveGit succeeded with an empty search path")
	}
	// An error that does not say how to proceed just moves the problem.
	if !strings.Contains(err.Error(), gitEnvOverride) {
		t.Errorf("error does not mention the escape hatch: %v", err)
	}
}

// LETSGO_GIT outranks the global config: an override set for one invocation
// must not be silently second-guessed by a machine-wide default.
func TestGitEnvOverrideOutranksGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "git")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, source, err := resolveGit(good, &config.Global{Path: "config.mod", Git: "/other/git"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != good || source != gitEnvOverride {
		t.Errorf("got %q from %q, want %q from %q", got, source, good, gitEnvOverride)
	}
}

// The global config's `git` directive outranks the system directory search,
// between the env override and it.
func TestGitGlobalConfigOutranksSystemDirs(t *testing.T) {
	dir := t.TempDir()
	pinned := filepath.Join(dir, "git")
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, source, err := resolveGit("", &config.Global{Path: "/etc/letsgo/config.mod", Git: pinned}, safeexec.SystemDirs())
	if err != nil {
		t.Fatal(err)
	}
	if got != pinned || source != "/etc/letsgo/config.mod" {
		t.Errorf("got %q from %q, want %q from the global config path", got, source, pinned)
	}
}

func TestGitGlobalConfigMustBeAbsoluteAndExecutable(t *testing.T) {
	if _, _, err := resolveGit("", &config.Global{Path: "config.mod", Git: "git"}, nil); err == nil {
		t.Error("a relative path was accepted from the global config")
	}

	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "git")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if _, _, err := resolveGit("", &config.Global{Path: "config.mod", Git: notExecutable}, nil); err == nil {
			t.Error("a non-executable path was accepted from the global config")
		}
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	bin := testGit(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=Test", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
