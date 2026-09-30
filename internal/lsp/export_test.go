package lsp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// Helpers shared by the in-package and external tests: this file is compiled
// into the test binary only, and is how lsp_test reaches them.

// PinnedTool installs content as an executable named name into dir and returns
// its digest, which is what a pin line would carry. On Windows the file gets
// an .exe suffix, without which PATH lookup would not find it.
func PinnedTool(t *testing.T, dir, name, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // a plugin must be executable
		t.Fatal(err)
	}
	digest, err := plugin.DigestOf(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// IsolatePlugins points the plugin store and PATH at empty temp directories,
// so no test sees the machine's own plugins.
func IsolatePlugins(t *testing.T) (storeDir, pathDir string) {
	t.Helper()
	storeDir, pathDir = t.TempDir(), t.TempDir()
	t.Setenv(pluginstore.StoreEnvOverride, storeDir)
	t.Setenv("PATH", pathDir)
	return storeDir, pathDir
}
