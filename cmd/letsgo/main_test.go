package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/manifest"
)

// demoMainGo is a program small enough to build in a test, but one that
// answers --version: planAndBuild's smoke test insists on that from anything
// it builds.
const demoMainGo = `package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("demo %s (%s) built %s\n", version, commit, date)
	}
}
`

// moduleFixture writes a minimal buildable module and commits it, so
// plan.Resolve has a real repository to work from.
//
// Driven as one shell-independent command list, with the identity given as
// -c flags rather than a GIT_AUTHOR_* environment: a test fixture belongs to
// this file, not copied from the shape another package's already has.
func moduleFixture(t *testing.T) string {
	t.Helper()
	return moduleFixtureWith(t, "", nil)
}

// moduleFixtureWith is moduleFixture with directives appended to its
// letsgo.mod and extra files committed beside it.
func moduleFixtureWith(t *testing.T, config string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    demoMainGo,
		"letsgo.mod": "build " + gobuild.Host().String() + "\n" + config,
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	identity := []string{"-c", "user.name=Test", "-c", "user.email=t@example.com"}
	for _, args := range [][]string{
		{"-C", dir, "init", "-q", "-b", "main"},
		{"-C", dir, "remote", "add", "origin", "https://github.com/you/demo.git"},
		{"-C", dir, "add", "."},
		append(append([]string{"-C", dir}, identity...), "commit", "-q", "-m", "feat: first release"),
		{"-C", dir, "tag", "v1.2.3"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func writeManifest(t *testing.T, dir, name, version, goVersion string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := json.Marshal(manifest.Manifest{
		Schema:  manifest.Schema,
		Version: version,
		Builder: manifest.Builder{Tool: "letsgo", Go: goVersion},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// captureStdout runs fn with os.Stdout redirected, and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = stdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Rounding everything to tenths reports a plan that finished in forty
// milliseconds as "0s", which reads like the tool did nothing.
func TestTookKeepsSubSecondDetail(t *testing.T) {
	if got := took(time.Now().Add(-40 * time.Millisecond)); got == 0 {
		t.Errorf("took = %v, want a measurable duration", got)
	}
	if got := took(time.Now().Add(-90 * time.Second)); got.Round(time.Second) != 90*time.Second {
		t.Errorf("took = %v", got)
	}
}
