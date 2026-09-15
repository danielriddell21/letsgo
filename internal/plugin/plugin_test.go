package plugin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plugin"
)

// fake writes an executable that echoes a fixed answer, and returns its
// digest. A shell script is enough: the contract is a subprocess reading JSON
// and writing JSON, not a Go program.
func fake(t *testing.T, body string) (dir, digest string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}

	dir = t.TempDir()
	path := filepath.Join(dir, "letsgo-fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return dir, "sha256:" + hex.EncodeToString(sum[:])
}

func TestRunSendsInputAndDecodesTheAnswer(t *testing.T) {
	dir, digest := fake(t, `cat > /dev/null; echo '{"archives":[{"name":"tools","binaries":["a","b"]}]}'`)
	t.Setenv("PATH", dir)

	var out plugin.ArchiveLayoutOutput
	err := plugin.Run(context.Background(),
		plugin.Plugin{
			Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest,
		},
		t.TempDir(), plugin.ArchiveLayoutInput{Project: "tools"}, &out)
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Archives) != 1 || out.Archives[0].Name != "tools" {
		t.Errorf("archives = %+v", out.Archives)
	}
}

// The pin is the whole contract: a plugin decides what gets built, so running
// a different program than the one recorded would produce a release nobody
// could account for.
func TestRunRefusesAProgramThatDoesNotMatchThePin(t *testing.T) {
	dir, _ := fake(t, `echo '{}'`)
	t.Setenv("PATH", dir)

	pinned := "sha256:" + strings.Repeat("0", 64)
	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: pinned},
		t.TempDir(), plugin.ArchiveLayoutInput{}, &plugin.ArchiveLayoutOutput{})

	if err == nil {
		t.Fatal("a plugin that does not match its pin should not have run")
	}
	if !strings.Contains(err.Error(), "the config pins") {
		t.Errorf("error = %q", err)
	}
}

func TestRunReportsWhatThePluginPrintedOnFailure(t *testing.T) {
	dir, digest := fake(t, `echo "the module builds no commands" >&2; exit 1`)
	t.Setenv("PATH", dir)

	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest},
		t.TempDir(), plugin.ArchiveLayoutInput{}, &plugin.ArchiveLayoutOutput{})

	if err == nil {
		t.Fatal("a failing plugin should be an error")
	}
	if !strings.Contains(err.Error(), "the module builds no commands") {
		t.Errorf("the plugin's own words were lost: %q", err)
	}
}

func TestRunRejectsAnUnknownHook(t *testing.T) {
	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: "not-a-hook", Command: "letsgo-fake", Digest: "sha256:x"},
		t.TempDir(), struct{}{}, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "not a hook") {
		t.Errorf("err = %v", err)
	}
}

func TestHooksAreAClosedSet(t *testing.T) {
	if !plugin.HookLDFlags.Valid() || !plugin.HookArchiveLayout.Valid() {
		t.Error("a documented hook is not valid")
	}
	if plugin.Hook("exec").Valid() {
		t.Error("an undocumented hook is valid")
	}
}
