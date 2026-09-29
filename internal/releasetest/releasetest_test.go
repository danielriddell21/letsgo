package releasetest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/releasetest"
)

func TestRepoWriteAndCommit(t *testing.T) {
	r := releasetest.NewRepo(t)
	r.Write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.Commit("v1.0.0")

	if _, err := os.Stat(filepath.Join(r.Dir, "go.mod")); err != nil {
		t.Fatalf("go.mod not written: %v", err)
	}
}

func TestRepoCommitWithNoTag(t *testing.T) {
	r := releasetest.NewRepo(t)
	r.Write("README.md", "hello\n")
	r.Commit("") // no tag argument: must not fail
}

func TestBuildProducesAReleaseResult(t *testing.T) {
	_, result := releasetest.Build(t, "github.com/you/demo", "v1.0.0")
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Artifacts) == 0 {
		t.Error("Artifacts is empty, want at least one built artifact")
	}
}
