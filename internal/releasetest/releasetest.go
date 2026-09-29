// Package releasetest builds a minimal, real release for tests that need
// release.Build's actual output — a source tree, a git tag, and the
// resulting manifest and archives — rather than a hand-rolled stand-in.
package releasetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
)

// MainGo is a module that reports its injected version, so a test built
// from it has something real to verify.
const MainGo = `package main

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

// Build writes a one-file module at the given path, commits and tags it,
// then runs it through the real plan/release pipeline. It fails t on any
// error, so callers can use its result directly.
func Build(t *testing.T, module, tag string) (dist string, result *release.Result) {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod":     "module " + module + "\n\ngo 1.24\n",
		"main.go":    MainGo,
		"letsgo.mod": "build " + gobuild.Host().String() + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "feat: first")
	run("tag", tag)

	p, err := plan.Resolve(ctx, plan.Options{Dir: dir})
	if err != nil || !p.OK() {
		t.Fatalf("plan: %v %+v", err, p.Checks)
	}

	dist = t.TempDir()
	result, err = release.Build(ctx, p, dist, "test", nil, nil)
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	return dist, result
}
