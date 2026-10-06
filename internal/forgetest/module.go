package forgetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
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

// Module writes a minimal buildable module of example.com/demo, tagged v1.2.3
// with origin at github.com/you/demo, and commits it, so plan.Resolve has a
// real repository to work from. It returns the directory.
func Module(tb testing.TB) string {
	tb.Helper()
	return ModuleWith(tb, "", nil)
}

// ModuleWith is Module with directives appended to its letsgo.mod and extra
// files committed beside it.
func ModuleWith(tb testing.TB, config string, extra map[string]string) string {
	tb.Helper()
	dir := tb.TempDir()

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
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			tb.Fatal(err)
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
		if out, err := exec.CommandContext(context.Background(), "git", args...).CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}
