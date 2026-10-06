package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/forgetest"

	"github.com/danielriddell21/letsgo/manifest"
)

// moduleFixture writes a minimal buildable module and commits it, so
// plan.Resolve has a real repository to work from.
func moduleFixture(t *testing.T) string {
	t.Helper()
	return forgetest.Module(t)
}

// moduleFixtureWith is moduleFixture with directives appended to its
// letsgo.mod and extra files committed beside it.
func moduleFixtureWith(t *testing.T, config string, extra map[string]string) string {
	t.Helper()
	return forgetest.ModuleWith(t, config, extra)
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
