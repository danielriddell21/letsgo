package plugin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plugin"
)

func TestResolveReportsEachState(t *testing.T) {
	noStore(t)
	dir, digest := fake(t, `echo '{}'`)
	onPath := func(t *testing.T) { t.Setenv("PATH", dir) }
	empty := func(t *testing.T) { t.Setenv("PATH", t.TempDir()) }

	tests := map[string]struct {
		setup  func(*testing.T)
		digest string
		want   plugin.State
	}{
		"on PATH with the pinned digest": {onPath, digest, plugin.Installed},
		"nowhere":                        {empty, digest, plugin.Missing},
		"on PATH, another build":         {onPath, "sha256:" + strings.Repeat("0", 64), plugin.Mismatch},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tt.setup(t)
			got := plugin.Resolve("letsgo-fake", tt.digest, t.TempDir())
			if got.State != tt.want {
				t.Fatalf("state = %v, want %v (%+v)", got.State, tt.want, got)
			}
			switch tt.want {
			case plugin.Installed:
				if got.Path != filepath.Join(dir, "letsgo-fake") {
					t.Errorf("path = %q", got.Path)
				}
			case plugin.Missing:
				if got.Err == nil {
					t.Error("a missing plugin should say why")
				}
			case plugin.Mismatch:
				if got.Digest != digest {
					t.Errorf("digest = %q, want what is on PATH, %q", got.Digest, digest)
				}
			}
		})
	}
}

func TestResolveReportsATamperedStoreEntryAsBroken(t *testing.T) {
	dir, digest := fake(t, `echo '{}'`)
	storeDir := t.TempDir()
	t.Setenv("LETSGO_PLUGIN_STORE", storeDir)
	t.Setenv("PATH", dir) // the right build is on PATH, and must not rescue it
	putInStore(t, storeDir, digest, "letsgo-fake", filepath.Join(dir, "letsgo-fake"))

	entry := filepath.Join(storeDir, "sha256", strings.TrimPrefix(digest, "sha256:"), "letsgo-fake")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := plugin.Resolve("letsgo-fake", digest, t.TempDir())
	if got.State != plugin.Broken || got.Err == nil || !strings.Contains(got.Err.Error(), "tampered") {
		t.Errorf("got %+v, want Broken for a tampered entry", got)
	}
}

func TestResolveDoesNotCreateTheStore(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never")
	t.Setenv("LETSGO_PLUGIN_STORE", missing)
	t.Setenv("PATH", t.TempDir())

	plugin.Resolve("letsgo-fake", "sha256:"+strings.Repeat("0", 64), t.TempDir())

	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("a lookup left a store behind: %v", err)
	}
}

// A relative command is a program inside the repository. It must be the same
// file whether it is hashed or run, from wherever the process was started.
func TestResolveAnchorsARelativeCommandToTheRepository(t *testing.T) {
	noStore(t)
	t.Setenv("PATH", t.TempDir())
	repo, digest := fake(t, `echo '{}'`)

	t.Chdir(t.TempDir())

	got := plugin.Resolve("./letsgo-fake", digest, repo)
	if got.State != plugin.Installed || got.Path != filepath.Join(repo, "letsgo-fake") {
		t.Errorf("got %+v, want the repository's own program", got)
	}
}

func TestRunExecutesTheFileItHashed(t *testing.T) {
	noStore(t)
	t.Setenv("PATH", t.TempDir())
	repo, digest := fake(t, `cat > /dev/null; echo '{"archives":[{"name":"x","binaries":["a"]}]}'`)

	// The process starts somewhere that holds a different file at the same
	// relative path: hashing that one and running the repository's would
	// defeat the pin.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "letsgo-fake"), []byte("#!/bin/sh\necho '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)

	var out plugin.ArchiveLayoutOutput
	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "./letsgo-fake", Digest: digest},
		repo, plugin.ArchiveLayoutInput{Project: "x"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Archives) != 1 {
		t.Errorf("ran the wrong file: %+v", out)
	}
}

func TestShort(t *testing.T) {
	tests := map[string]string{
		"sha256:0123456789abcdef": "sha256:0123456789ab…",
		"sha256:abc":              "sha256:abc",
		"plain":                   "plain",
	}
	for in, want := range tests {
		if got := plugin.Short(in); got != want {
			t.Errorf("Short(%q) = %q, want %q", in, got, want)
		}
	}
}
