package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/releasetest"
)

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

func TestParseDiagnosticsCarriesTheSuggestion(t *testing.T) {
	diags := parseDiagnostics("letsgo.mod", "bulid linux/amd64\n")
	if len(diags) != 1 || diags[0].Data == nil {
		t.Fatalf("diags = %+v, want one with a suggestion", diags)
	}
	if got, want := *diags[0].Data, (Suggestion{Wrong: "bulid", Suggest: "build"}); got != want {
		t.Errorf("Data = %+v, want %+v", got, want)
	}
	if r := diags[0].Range; r.End.Character-r.Start.Character != len("bulid") {
		t.Errorf("Range = %+v, want it to cover the word", r)
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
	r := releasetest.NewRepo(t)
	r.Write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.Write("main.go", "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	r.Write("letsgo.mod", "build "+gobuild.Host().String()+"\nbudget windows/arm64 10MB\n")
	r.Commit("v1.0.0")

	path := filepath.Join(r.Dir, "letsgo.mod")
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
