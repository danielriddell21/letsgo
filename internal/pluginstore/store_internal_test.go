package pluginstore

import (
	"path/filepath"
	"testing"
)

// The global config's `plugins <dir>` directive is used when no explicit dir
// and no env override are given.
func TestOpenHonoursGlobalPluginsDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	s, err := Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.dir != dir {
		t.Errorf("dir = %q, want %q", s.dir, dir)
	}
}

// An explicit dir outranks the global config.
func TestOpenExplicitDirOutranksGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "/should/not/be/used")
	if err != nil {
		t.Fatal(err)
	}
	if s.dir != dir {
		t.Errorf("dir = %q, want %q", s.dir, dir)
	}
}

// StoreEnvOverride outranks the global config: it names the store this one
// invocation must use.
func TestOpenEnvOverrideOutranksGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(StoreEnvOverride, dir)

	s, err := Open("", "/should/not/be/used")
	if err != nil {
		t.Fatal(err)
	}
	if s.dir != dir {
		t.Errorf("dir = %q, want %q", s.dir, dir)
	}
}
