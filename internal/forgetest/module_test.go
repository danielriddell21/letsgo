package forgetest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/forgetest"
)

func read(t *testing.T, dir, name string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput() // NOSONAR: a test fixture runs the developer's own git
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The module is the repository every release test is made in: it has to be a
// real one, built, tagged and with an origin, or plan.Resolve has nothing to
// resolve.
func TestModuleIsATaggedRepositoryWithAnOrigin(t *testing.T) {
	dir := forgetest.Module(t)

	if got := read(t, dir, "go.mod"); !strings.Contains(got, "module example.com/demo") {
		t.Errorf("go.mod = %q", got)
	}
	if got := read(t, dir, "letsgo.mod"); !strings.HasPrefix(got, "build ") {
		t.Errorf("letsgo.mod = %q, want it to start with the host's build", got)
	}
	if got := gitOut(t, dir, "tag"); got != "v1.2.3" {
		t.Errorf("tags = %q, want v1.2.3", got)
	}
	if got := gitOut(t, dir, "remote", "get-url", "origin"); got != "https://github.com/you/demo.git" {
		t.Errorf("origin = %q", got)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != "" {
		t.Errorf("worktree is dirty:\n%s", got)
	}
}

func TestModuleWithAddsDirectivesAndFiles(t *testing.T) {
	dir := forgetest.ModuleWith(t, "release draft=true\n", map[string]string{"docs/README.md": "hello"})

	if got := read(t, dir, "letsgo.mod"); !strings.HasSuffix(got, "release draft=true\n") {
		t.Errorf("letsgo.mod = %q, want the directive appended", got)
	}
	if got := read(t, dir, "docs/README.md"); got != "hello" {
		t.Errorf("docs/README.md = %q", got)
	}
	if got := gitOut(t, dir, "ls-files", "docs/README.md"); got != "docs/README.md" {
		t.Errorf("extra file is not committed: %q", got)
	}
}
