package pluginstore

import (
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

// The global config's `plugins <dir>` directive is used when no explicit dir
// and no env override are given.
func TestOpenWithHonoursGlobalPluginsDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	s, err := openWith("", &config.Global{PluginsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if s.dir != dir {
		t.Errorf("dir = %q, want %q", s.dir, dir)
	}
}

// An explicit dir outranks the global config.
func TestOpenWithExplicitDirOutranksGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := openWith(dir, &config.Global{PluginsDir: "/should/not/be/used"})
	if err != nil {
		t.Fatal(err)
	}
	if s.dir != dir {
		t.Errorf("dir = %q, want %q", s.dir, dir)
	}
}

// StoreEnvOverride outranks the global config: it names the store this one
// invocation must use.
func TestOpenWithEnvOverrideOutranksGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(StoreEnvOverride, dir)

	s, err := openWith("", &config.Global{PluginsDir: "/should/not/be/used"})
	if err != nil {
		t.Fatal(err)
	}
	if s.dir != dir {
		t.Errorf("dir = %q, want %q", s.dir, dir)
	}
}
