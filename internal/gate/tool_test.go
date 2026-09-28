package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

// A global `tool <name> <path>` override wins outright: it names the tool to
// run, not another place to search.
func TestFindWithUsesTheGlobalOverride(t *testing.T) {
	dir := t.TempDir()
	pinned := filepath.Join(dir, "govulncheck")
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findWith("govulncheck", "go install ...", &config.Global{
		Path:  "config.mod",
		Tools: map[string]string{"govulncheck": pinned},
	})
	if err != nil {
		t.Fatalf("findWith: %v", err)
	}
	if got != pinned {
		t.Errorf("got %q, want %q", got, pinned)
	}
}

func TestFindWithFallsThroughWithoutAnOverride(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	t.Setenv("PATH", "")

	if _, err := findWith("definitely-not-installed", "go install example.com/x", &config.Global{}); err == nil {
		t.Fatal("expected the tool to be reported missing")
	}
}

func TestFindWithGlobalOverrideMustBeAbsoluteAndExecutable(t *testing.T) {
	if _, err := findWith("govulncheck", "install", &config.Global{
		Path:  "config.mod",
		Tools: map[string]string{"govulncheck": "govulncheck"},
	}); err == nil {
		t.Error("a relative path was accepted from the global config")
	}

	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "govulncheck")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findWith("govulncheck", "install", &config.Global{
		Path:  "config.mod",
		Tools: map[string]string{"govulncheck": notExecutable},
	}); err == nil {
		t.Error("a non-executable path was accepted from the global config")
	}
}
