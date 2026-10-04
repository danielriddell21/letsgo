package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/git"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitRun runs git in dir with a fixed author/committer identity and date, so
// tests built on it are deterministic.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// testGit is the git binary letsgo itself would resolve here.
func testGit(t *testing.T) string {
	t.Helper()
	bin, _, err := git.Binary(nil)
	if err != nil {
		t.Skipf("no git in a system directory: %v", err)
	}
	return bin
}
