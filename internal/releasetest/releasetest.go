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

	"github.com/danielriddell21/letsgo/internal/git"

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

// Repo is a scratch git repository for tests that need real plan/discover
// behaviour rather than a hand-rolled stand-in — the shared shape every
// package's own git-fixture helper otherwise reimplements.
type Repo struct {
	t   *testing.T
	Dir string
}

// NewRepo creates an empty repository in a fresh temp directory.
func NewRepo(t *testing.T) *Repo {
	t.Helper()
	r := &Repo{t: t, Dir: t.TempDir()}
	r.Git("init", "-q", "-b", "main")
	return r
}

// Git runs a git command in the repo, with a fixed author/committer
// identity so commits are reproducible across machines and CI runners.
func (r *Repo) Git(args ...string) {
	r.t.Helper()
	gitBin, _, err := git.Binary(nil)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), gitBin, args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// Write creates or overwrites a file in the repo, relative to its root.
func (r *Repo) Write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.Dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages and commits every file written so far, and tags the commit
// when tag is non-empty.
func (r *Repo) Commit(tag string) {
	r.t.Helper()
	r.Git("add", ".")
	r.Git("commit", "-q", "-m", "commit")
	if tag != "" {
		r.Git("tag", tag)
	}
}

// Build writes a one-file module at the given path, commits and tags it,
// then runs it through the real plan/release pipeline. It fails t on any
// error, so callers can use its result directly.
func Build(t *testing.T, module, tag string) (dist string, result *release.Result) {
	t.Helper()
	r := NewRepo(t)

	files := map[string]string{
		"go.mod":     "module " + module + "\n\ngo 1.24\n",
		"main.go":    MainGo,
		"letsgo.mod": "build " + gobuild.Host().String() + "\n",
	}
	for name, content := range files {
		r.Write(name, content)
	}
	r.Git("add", ".")
	r.Git("commit", "-q", "-m", "feat: first")
	r.Git("tag", tag)

	ctx := context.Background()
	p, err := plan.Resolve(ctx, plan.Options{Dir: r.Dir})
	if err != nil || !p.OK() {
		t.Fatalf("plan: %v %+v", err, p.Checks)
	}

	dist = t.TempDir()
	result, err = release.Build(ctx, release.BuildOptions{Plan: p, Dir: dist, ToolVersion: "test"})
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	return dist, result
}
