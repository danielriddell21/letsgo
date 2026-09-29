package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
)

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *repo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r *repo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(tag string) {
	r.t.Helper()
	r.git("add", ".")
	r.git("commit", "-q", "-m", "commit")
	if tag != "" {
		r.git("tag", tag)
	}
}

func TestKindOf(t *testing.T) {
	tests := []struct {
		path string
		want fileKind
	}{
		{"/repo/letsgo.mod", kindRepo},
		{filepath.Join("a", "b", "letsgo.mod"), kindRepo},
		{"/home/config.mod", kindGlobal},
		{"/repo/.letsgo/letsgo-cask.mod", kindSyntaxOnly},
		{"/repo/README.md", kindSyntaxOnly},
	}
	for _, tt := range tests {
		if got := kindOf(tt.path); got != tt.want {
			t.Errorf("kindOf(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestParseDiagnosticsReportsASyntaxError(t *testing.T) {
	diags := parseDiagnostics("letsgo.mod", "build (\n  linux/amd64\n")
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	if diags[0].Severity != SeverityError {
		t.Errorf("Severity = %d, want %d", diags[0].Severity, SeverityError)
	}
}

func TestParseDiagnosticsReportsADecodeErrorRepo(t *testing.T) {
	diags := parseDiagnostics("letsgo.mod", "not-a-real-directive foo\n")
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
}

func TestParseDiagnosticsIsSyntaxOnlyForPluginConfig(t *testing.T) {
	path := filepath.Join(".letsgo", "letsgo-cask.mod")
	diags := parseDiagnostics(path, "not-a-real-directive foo\n")
	if len(diags) != 0 {
		t.Errorf("diags = %+v, want none: an unrecognised word is fine outside letsgo.mod/config.mod", diags)
	}
}

func TestParseDiagnosticsDecodesGlobalConfig(t *testing.T) {
	diags := parseDiagnostics("config.mod", "not-a-real-directive foo\n")
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}

	if diags := parseDiagnostics("config.mod", "go /usr/bin/go\n"); len(diags) != 0 {
		t.Errorf("diags = %+v, want none for a valid global directive", diags)
	}
}

func TestPlanDiagnosticsReportsAFailingCheckWithPosition(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build "+gobuild.Host().String()+"\nbudget windows/arm64 10MB\n")
	r.commit("v1.0.0")

	path := filepath.Join(r.dir, "letsgo.mod")
	diags := planDiagnostics(context.Background(), path)
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	if diags[0].Range.Start.Line != 1 {
		t.Errorf("Range.Start.Line = %d, want 1 (the budget line)", diags[0].Range.Start.Line)
	}
	if !strings.Contains(diags[0].Message, "windows/arm64") {
		t.Errorf("Message = %q, want it to name windows/arm64", diags[0].Message)
	}
}

func TestPlanDiagnosticsReturnsNilWhenNotResolvable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "letsgo.mod")
	if diags := planDiagnostics(context.Background(), path); diags != nil {
		t.Errorf("diags = %+v, want nil for a directory with no repo", diags)
	}
}
