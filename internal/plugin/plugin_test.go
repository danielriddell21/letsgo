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
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// noStore keeps a test's plugin resolution off the real machine's store,
// which every test not specifically about the store should do: none of them
// mean to depend on, or leave behind, whatever else is on this machine.
func noStore(t *testing.T) {
	t.Helper()
	t.Setenv(pluginstore.StoreEnvOverride, t.TempDir())
}

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
	noStore(t)
	dir, digest := fake(t, `cat > /dev/null; echo '{"archives":[{"name":"tools","binaries":["a","b"]}]}'`)
	t.Setenv("PATH", dir)

	var out plugin.ArchiveLayoutOutput
	err := plugin.Run(context.Background(),
		plugin.Plugin{
			Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest,
		},
		t.TempDir(), "", plugin.ArchiveLayoutInput{Project: "tools"}, &out)
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
	noStore(t)
	dir, _ := fake(t, `echo '{}'`)
	t.Setenv("PATH", dir)

	pinned := "sha256:" + strings.Repeat("0", 64)
	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: pinned},
		t.TempDir(), "", plugin.ArchiveLayoutInput{}, &plugin.ArchiveLayoutOutput{})

	if err == nil {
		t.Fatal("a plugin that does not match its pin should not have run")
	}
	if !strings.Contains(err.Error(), "the config pins") {
		t.Errorf("error = %q", err)
	}
}

func TestRunReportsWhatThePluginPrintedOnFailure(t *testing.T) {
	noStore(t)
	dir, digest := fake(t, `echo "the module builds no commands" >&2; exit 1`)
	t.Setenv("PATH", dir)

	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest},
		t.TempDir(), "", plugin.ArchiveLayoutInput{}, &plugin.ArchiveLayoutOutput{})

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
		t.TempDir(), "", struct{}{}, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "not a hook") {
		t.Errorf("err = %v", err)
	}
}

// The store is checked before PATH, and a plugin found there needs no PATH
// entry at all — that is the whole point of it.
func TestRunFindsAPluginInTheStoreWithoutPATH(t *testing.T) {
	dir, digest := fake(t, `echo '{}'`)
	t.Setenv("PATH", t.TempDir())

	storeDir := t.TempDir()
	t.Setenv(pluginstore.StoreEnvOverride, storeDir)
	putInStore(t, storeDir, digest, "letsgo-fake", filepath.Join(dir, "letsgo-fake"))

	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest},
		t.TempDir(), "", struct{}{}, &struct{}{})
	if err != nil {
		t.Fatal(err)
	}
}

// Two repositories pinning two different digests of the same-named plugin
// must both work on one machine, at once, with neither reinstalling.
func TestRunResolvesTwoDigestsOfTheSameNameFromTheStore(t *testing.T) {
	dirA, digestA := fake(t, `echo '{"archives":[{"name":"a"}]}'`)
	dirB, digestB := fake(t, `echo '{"archives":[{"name":"b"}]}'`)
	t.Setenv("PATH", t.TempDir())

	storeDir := t.TempDir()
	t.Setenv(pluginstore.StoreEnvOverride, storeDir)
	putInStore(t, storeDir, digestA, "letsgo-fake", filepath.Join(dirA, "letsgo-fake"))
	putInStore(t, storeDir, digestB, "letsgo-fake", filepath.Join(dirB, "letsgo-fake"))

	var outA, outB plugin.ArchiveLayoutOutput
	if err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digestA},
		t.TempDir(), "", struct{}{}, &outA); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digestB},
		t.TempDir(), "", struct{}{}, &outB); err != nil {
		t.Fatal(err)
	}

	if len(outA.Archives) != 1 || outA.Archives[0].Name != "a" {
		t.Errorf("A resolved to %+v", outA)
	}
	if len(outB.Archives) != 1 || outB.Archives[0].Name != "b" {
		t.Errorf("B resolved to %+v", outB)
	}
}

// A store entry that has been altered on disk must fail outright, never fall
// back to PATH as if the store had simply missed.
func TestRunFailsOnATamperedStoreEntry(t *testing.T) {
	dir, digest := fake(t, `echo '{}'`)

	storeDir := t.TempDir()
	t.Setenv(pluginstore.StoreEnvOverride, storeDir)
	putInStore(t, storeDir, digest, "letsgo-fake", filepath.Join(dir, "letsgo-fake"))

	hex := strings.TrimPrefix(digest, "sha256:")
	tampered := filepath.Join(storeDir, "sha256", hex, "letsgo-fake")
	if err := os.WriteFile(tampered, []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest},
		t.TempDir(), "", struct{}{}, &struct{}{})
	if err == nil {
		t.Fatal("a tampered store entry should not have run")
	}
	if !strings.Contains(err.Error(), "tampered") {
		t.Errorf("err = %v", err)
	}
}

// putInStore copies src into store as name at digest, the way `plugin
// install` would have.
func putInStore(t *testing.T, storeDir, digest, name, src string) {
	t.Helper()
	store, err := pluginstore.Open(storeDir, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(digest, name, data); err != nil {
		t.Fatal(err)
	}
}

func TestHooksAreAClosedSet(t *testing.T) {
	if !plugin.HookLDFlags.Valid() || !plugin.HookArchiveLayout.Valid() || !plugin.HookTapFiles.Valid() {
		t.Error("a documented hook is not valid")
	}
	if plugin.Hook("exec").Valid() {
		t.Error("an undocumented hook is valid")
	}
}

// A credential that can publish to another repository has no reason to be
// visible to a plugin that only renders files for core to write.
func TestRunStripsTokensFromTheTapFilesHookEnv(t *testing.T) {
	noStore(t)
	dir, digest := fake(t, `
if [ -n "$GITHUB_TOKEN$GH_TOKEN$LETSGO_TAP_TOKEN$LETSGO_RELEASE_TOKEN" ]; then
  echo "a token reached the plugin" >&2
  exit 1
fi
echo '{"files":[]}'`)
	t.Setenv("PATH", dir)
	t.Setenv("GITHUB_TOKEN", "secret")
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("LETSGO_TAP_TOKEN", "secret")
	t.Setenv("LETSGO_RELEASE_TOKEN", "secret")

	var out plugin.TapFilesOutput
	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookTapFiles, Command: "letsgo-fake", Digest: digest},
		t.TempDir(), "", plugin.TapFilesInput{}, &out)
	if err != nil {
		t.Fatal(err)
	}
}

// Every other hook keeps the whole environment: only tap-files talks about a
// tap, so only it needs the token kept out.
func TestRunLeavesOtherHooksEnvWhole(t *testing.T) {
	noStore(t)
	dir, digest := fake(t, `
if [ -z "$GITHUB_TOKEN" ]; then
  echo "the token was stripped" >&2
  exit 1
fi
echo '{}'`)
	t.Setenv("PATH", dir)
	t.Setenv("GITHUB_TOKEN", "secret")

	err := plugin.Run(context.Background(),
		plugin.Plugin{Hook: plugin.HookArchiveLayout, Command: "letsgo-fake", Digest: digest},
		t.TempDir(), "", plugin.ArchiveLayoutInput{}, &plugin.ArchiveLayoutOutput{})
	if err != nil {
		t.Fatal(err)
	}
}
