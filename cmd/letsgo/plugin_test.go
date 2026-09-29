package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/forgetest"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
	"github.com/danielriddell21/letsgo/selfupdate"
)

func TestDefaultPluginRepoWithFallsBackWithoutAGlobalOverride(t *testing.T) {
	if got := defaultPluginRepoWith(&config.Global{}); got != pluginRepo {
		t.Errorf("got %q, want %q", got, pluginRepo)
	}
}

func TestDefaultPluginRepoWithHonoursTheGlobalOverride(t *testing.T) {
	got := defaultPluginRepoWith(&config.Global{PluginRepo: "acme/letsgo-plugins-mirror"})
	if got != "acme/letsgo-plugins-mirror" {
		t.Errorf("got %q, want the global override", got)
	}
}

// noStore points plugin installation at a scratch store for the duration of
// a test, so nothing it writes or reads touches the real machine's store.
func noStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(pluginstore.StoreEnvOverride, dir)
	return dir
}

// withStandalonePlugin temporarily adds a known plugin that answers no hook,
// so the reporting that treats one specially can be exercised even though
// nothing in the real catalogue is like this today.
func withStandalonePlugin(t *testing.T, command string) {
	t.Helper()
	orig := plugin.Known
	plugin.Known = append(append([]plugin.KnownPlugin{}, orig...), plugin.KnownPlugin{Command: command})
	t.Cleanup(func() { plugin.Known = orig })
}

func TestSplitPluginRef(t *testing.T) {
	for _, tc := range []struct{ ref, name, version string }{
		{"letsgo-multi", "letsgo-multi", ""},
		{"letsgo-multi@v0.2.0", "letsgo-multi", "v0.2.0"},
		{"letsgo-multi@latest", "letsgo-multi", "latest"},
		{"", "", ""},
	} {
		name, version := splitPluginRef(tc.ref)
		if name != tc.name || version != tc.version {
			t.Errorf("splitPluginRef(%q) = %q, %q; want %q, %q",
				tc.ref, name, version, tc.name, tc.version)
		}
	}
}

// The line printed after an install is the one that goes into letsgo.mod, so
// it has to be copy-pasteable when the hook is known.
func TestDescribePinFillsTheHookFromTheConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")

	var out bytes.Buffer
	describePin(&out, "letsgo-multi", &selfupdate.Update{
		Version:      "0.2.0",
		BinarySHA256: strings.Repeat("b", 64),
	})

	want := "plugin archive-layout letsgo-multi v0.2.0 sha256:" + strings.Repeat("b", 64)
	if !strings.Contains(out.String(), want) {
		t.Errorf("describePin printed:\n%s\nwant a line %q", out.String(), want)
	}
	if strings.Contains(out.String(), "<hook>") {
		t.Error("the hook was in the config and should not have been left blank")
	}
}

// letsgo does not guess a hook it has not been told, for a plugin it does not
// publish itself. A placeholder the user has to fill is honest; a guessed
// hook that is wrong is a release nobody can account for.
func TestDescribePinLeavesAnUnknownHookBlank(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n")

	var out bytes.Buffer
	describePin(&out, "letsgo-third-party", &selfupdate.Update{
		Version:      "0.2.0",
		BinarySHA256: strings.Repeat("c", 64),
	})

	if !strings.Contains(out.String(), "plugin <hook> letsgo-third-party v0.2.0") {
		t.Errorf("describePin printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "the plugin documents") {
		t.Error("an unfilled hook should say where to find it")
	}
}

// A plugin letsgo publishes itself has a documented hook, so installing it
// fresh should fill that in rather than leave a placeholder; one that
// answers no hook must not be told to pin itself; and upgrading a plugin
// already pinned must never silently move it to a different hook, even if
// the catalogue disagreed.
func TestDescribePinUsesTheCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name, plugin, config, want, avoid string
	}{
		{
			"fills a known plugin's hook", "letsgo-env", "",
			"plugin ldflags letsgo-env v0.2.0", "<hook>",
		},
		{
			"prefers the config over the catalogue", "letsgo-env",
			"build linux/amd64\nplugin ldflags letsgo-env v0.1.0 sha256:" + strings.Repeat("a", 64) + "\n",
			"plugin ldflags letsgo-env v0.2.0", "",
		},
		{
			"reports a standalone plugin", "letsgo-standalone", "",
			"does not answer a hook", "pin it in letsgo.mod",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin == "letsgo-standalone" {
				withStandalonePlugin(t, tc.plugin)
			}
			t.Chdir(t.TempDir())
			if tc.config != "" {
				write(t, "letsgo.mod", tc.config)
			}

			var out bytes.Buffer
			describePin(&out, tc.plugin, &selfupdate.Update{
				Version:      "0.2.0",
				BinarySHA256: strings.Repeat("c", 64),
			})

			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("describePin printed:\n%s\nwant %q", out.String(), tc.want)
			}
			if tc.avoid != "" && strings.Contains(out.String(), tc.avoid) {
				t.Errorf("describePin printed:\n%s\nwant it not to contain %q", out.String(), tc.avoid)
			}
		})
	}
}

// Installing a plugin in a directory that has no letsgo.mod is a normal thing
// to do, and must not fail.
func TestPinnedHookToleratesNoConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	if hook := pinnedHook("letsgo-multi"); hook != "" {
		t.Errorf("pinnedHook = %q, want empty", hook)
	}
}

func TestPluginStatusReportsAMismatch(t *testing.T) {
	dir := t.TempDir()
	writeProgram(t, dir, "letsgo-fake", "the wrong bytes")

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	status, ok := pluginStatus(config.Plugin{
		Hook: "ldflags", Command: "letsgo-fake",
		Version: "v0.1.0", Digest: "sha256:" + strings.Repeat("d", 64),
	})
	if ok {
		t.Fatal("a plugin whose digest differs from the pin is not ok")
	}
	if !strings.Contains(status, "dddddddddddd") {
		t.Errorf("status = %q, want it to name the pinned digest", status)
	}
}

func TestPluginStatusReportsAMissingPlugin(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	status, ok := pluginStatus(config.Plugin{
		Hook: "ldflags", Command: "letsgo-absent",
		Version: "v0.1.0", Digest: "sha256:" + strings.Repeat("e", 64),
	})
	if ok || !strings.Contains(status, "not installed") {
		t.Errorf("status = %q, ok = %v", status, ok)
	}
}

// An interrupted install must leave the old plugin, never half of a new one,
// so the write goes through a rename.
func TestWriteExecutableReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "letsgo-multi")
	write(t, path, "old")

	if err := writeExecutable(path, []byte("new")); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}

	// The temporary file is written beside the target; none may survive.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".letsgo-plugin-") {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

// writeProgram creates a file LookPath will actually find, and returns its
// path.
//
// The two platforms disagree about what makes a file a program, and a test
// that satisfies only one of them passes only on one of them: Windows wants an
// extension from PATHEXT, and Unix wants the execute bit.
func writeProgram(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, executableName(name))
	write(t, path, content)

	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// executableName is what a file has to be called for LookPath to consider it
// at all: on Windows, one of the extensions PATHEXT lists.
func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListPluginsReportsEveryPin(t *testing.T) {
	dir := t.TempDir()
	writeProgram(t, dir, "letsgo-multi", "the multi plugin")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	digest, err := plugin.DigestOf(filepath.Join(dir, executableName("letsgo-multi")))
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.2.0 "+digest+"\n"+
		"plugin ldflags letsgo-env v0.2.0 sha256:"+strings.Repeat("f", 64)+"\n")

	var out bytes.Buffer
	if err := listPlugins(&out, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	// The pin that is satisfied, and the one that is not.
	if !strings.Contains(got, "archive-layout") || !strings.Contains(got, "ok  ") {
		t.Errorf("a satisfied pin should be reported ok:\n%s", got)
	}
	if !strings.Contains(got, "letsgo-env") || !strings.Contains(got, "not installed") {
		t.Errorf("a missing plugin should be reported:\n%s", got)
	}
	// An unmet pin is worth telling the reader how to fix.
	if !strings.Contains(got, "letsgo plugin install") {
		t.Errorf("an unmet pin should name the remedy:\n%s", got)
	}
}

// letsgo plugin list --json reports the same resolution as the text table,
// in its own schema-versioned wire form.
func TestListPluginsJSON(t *testing.T) {
	dir := t.TempDir()
	writeProgram(t, dir, "letsgo-multi", "the multi plugin")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	digest, err := plugin.DigestOf(filepath.Join(dir, executableName("letsgo-multi")))
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.2.0 "+digest+"\n"+
		"plugin ldflags letsgo-env v0.2.0 sha256:"+strings.Repeat("f", 64)+"\n")

	var out bytes.Buffer
	if err := listPlugins(&out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, want := range []string{
		`"schema": 1`,
		`"hook": "archive-layout"`,
		`"command": "letsgo-multi"`,
		`"ok": true`,
		`"hook": "ldflags"`,
		`"command": "letsgo-env"`,
		`"ok": false`,
		`"status": "not installed"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listPlugins JSON output = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "ok  ") {
		t.Errorf("listPlugins JSON output = %q, want no text-report padding in status", got)
	}
}

// A repository with no plugins is not an error, and should not print a table.
func TestListPluginsWithNoPins(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n")

	var out bytes.Buffer
	if err := listPlugins(&out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pins no plugins") {
		t.Errorf("out = %q", out.String())
	}
}

// plugin list --available answers "is there already a plugin for this"
// without needing a letsgo.mod at all: it lists what letsgo publishes, not
// what this repository pins.
func TestListAvailablePluginsListsTheCatalogue(t *testing.T) {
	withStandalonePlugin(t, "letsgo-standalone")

	var out bytes.Buffer
	if err := listAvailablePlugins(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	if !strings.Contains(got, "archive-layout") || !strings.Contains(got, "letsgo-multi") {
		t.Errorf("out = %q, want letsgo-multi under archive-layout", got)
	}
	if !strings.Contains(got, "tap-files") || !strings.Contains(got, "letsgo-cask") {
		t.Errorf("out = %q, want letsgo-cask under tap-files", got)
	}
	if !strings.Contains(got, "(standalone)") || !strings.Contains(got, "letsgo-standalone") {
		t.Errorf("out = %q, want letsgo-standalone marked standalone", got)
	}
}

// --available must work with no letsgo.mod in sight: it lists what could be
// pinned, not what already is.
func TestRunPluginListAvailableNeedsNoConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := runPluginList([]string{"--available"}); err != nil {
		t.Errorf("runPluginList(--available) = %v", err)
	}
}

func TestListPluginsReportsAMissingConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	if err := listPlugins(&out, false); err == nil {
		t.Fatal("no letsgo.mod should be an error for list, which has nothing to report without one")
	}
}

func TestInstallDirPrefersTheOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")

	got, err := installDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("installDir = %q, want %q", got, dir)
	}
	// It has to exist afterwards, or the install that follows cannot write.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("installDir did not create %s: %v", dir, err)
	}
}

func TestInstallDirFallsBackToGOBIN(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOBIN", dir)

	got, err := installDir(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("installDir = %q, want GOBIN %q", got, dir)
	}
}

func TestInstallDirFallsBackToGOPATHBin(t *testing.T) {
	gopath := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", gopath)

	got, err := installDir(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(gopath, "bin"); got != want {
		t.Errorf("installDir = %q, want %q", got, want)
	}
}

// The environment wins without shelling out, which is what makes the fallback
// to `go env` affordable.
func TestGoEnvPrefersTheEnvironment(t *testing.T) {
	t.Setenv("GOBIN", "/somewhere/particular")

	if got := goEnv(context.Background(), "GOBIN"); got != "/somewhere/particular" {
		t.Errorf("goEnv = %q", got)
	}
}

func TestRunPluginHelp(t *testing.T) {
	if err := runPlugin([]string{"help"}); err != nil {
		t.Errorf("runPlugin(help) = %v", err)
	}
}

// Every verb in the usage text has to be reachable, and every alias has to
// reach the same place as the name it aliases.
func TestCommandsCoverTheUsage(t *testing.T) {
	for _, name := range []string{
		"plan", "build", "release", "verify", "diff",
		"yank", "update", "plugin", "tag", "fmt", "version", "help",
	} {
		if commands[name] == nil {
			t.Errorf("no command registered for %q", name)
		}
	}
	for _, alias := range []string{"--version", "-version", "-v"} {
		if commands[alias] == nil {
			t.Errorf("no command registered for %q", alias)
		}
	}
	if err := runVersion(nil); err != nil {
		t.Error(err)
	}
	if err := runHelp(nil); err != nil {
		t.Error(err)
	}
}

// The whole point of the command: the executable that lands in the store is
// the one the release's manifest describes.
func TestInstallPluginWritesTheVerifiedBinary(t *testing.T) {
	storeDir := noStore(t)
	f := forgetest.New(t, "you/plugins", "v0.2.0")
	f.PublishCommands(t, "letsgo-plugins", "0.2.0", "letsgo-multi", "letsgo-env")

	t.Chdir(t.TempDir())

	options := f.Options()
	options.Binary = "letsgo-env"

	var out bytes.Buffer
	if err := installPlugin(context.Background(), &out, "letsgo-env", options, "", false); err != nil {
		t.Fatal(err)
	}

	digest := "sha256:" + forgetest.Sum([]byte(forgetest.Content("letsgo-env")))

	// The named plugin, not whichever artifact the platform matched first.
	store, err := pluginstore.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	path, ok, err := store.Lookup(digest, "letsgo-env")
	if err != nil || !ok {
		t.Fatalf("Lookup = %q, %v, %v", path, ok, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != forgetest.Content("letsgo-env") {
		t.Errorf("installed %q", got)
	}

	// The pin it prints has to carry the digest of what it just wrote.
	if !strings.Contains(out.String(), digest) {
		t.Errorf("printed:\n%s\nwant the pin to name %s", out.String(), digest)
	}
	if !strings.Contains(out.String(), "installed letsgo-env v0.2.0") {
		t.Errorf("printed:\n%s", out.String())
	}
}

// --link additionally puts a copy where PATH will find it, for running the
// plugin by hand.
func TestInstallPluginWithLinkAlsoPlacesACopyOnPATH(t *testing.T) {
	noStore(t)
	f := forgetest.New(t, "you/plugins", "v0.2.0")
	f.PublishCommands(t, "letsgo-plugins", "0.2.0", "letsgo-multi")

	t.Chdir(t.TempDir())
	linkDir := t.TempDir()

	options := f.Options()
	options.Binary = "letsgo-multi"

	var out bytes.Buffer
	if err := installPlugin(context.Background(), &out, "letsgo-multi", options, linkDir, true); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(linkDir, "letsgo-multi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != forgetest.Content("letsgo-multi") {
		t.Errorf("linked %q", got)
	}
}

// A tampered archive must not reach the store at all.
func TestInstallPluginRefusesATamperedArchive(t *testing.T) {
	storeDir := noStore(t)
	f := forgetest.New(t, "you/plugins", "v0.2.0")
	f.PublishCommands(t, "letsgo-plugins", "0.2.0", "letsgo-multi")
	for name := range f.Archives {
		f.Archives[name] = []byte("not the archive that was published")
	}

	t.Chdir(t.TempDir())

	options := f.Options()
	options.Binary = "letsgo-multi"

	err := installPlugin(context.Background(), io.Discard, "letsgo-multi", options, "", false)
	if err == nil {
		t.Fatal("a tampered archive should be refused")
	}

	store, err := pluginstore.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.Entries()
	if err != nil || len(entries) != 0 {
		t.Errorf("nothing should have been written: entries = %+v, err = %v", entries, err)
	}
}

// Installing an exact version is the pinned-plugin workflow, and must not be
// treated as an update check.
func TestInstallPluginByTag(t *testing.T) {
	noStore(t)
	f := forgetest.New(t, "you/plugins", "v0.1.0")
	f.PublishCommands(t, "letsgo-plugins", "0.1.0", "letsgo-multi")

	t.Chdir(t.TempDir())

	options := f.Options()
	options.Binary = "letsgo-multi"
	options.Tag = "v0.1.0"

	var out bytes.Buffer
	if err := installPlugin(context.Background(), &out, "letsgo-multi", options, "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "v0.1.0") {
		t.Errorf("printed:\n%s", out.String())
	}
}

// More than one positional argument is ambiguous: install one plugin, or
// every pin, never a chosen few.
func TestRunPluginInstallRejectsMoreThanOnePlugin(t *testing.T) {
	if err := runPluginInstall([]string{"letsgo-multi", "letsgo-env"}); err == nil {
		t.Error("runPluginInstall with two plugins should have failed")
	}
}

// With no arguments, install falls through to every pin in letsgo.mod
// (CD-4) — an empty config is reported rather than treated as an error.
func TestInstallAllPinsReportsNoPins(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n")

	var out bytes.Buffer
	if err := installAllPins(context.Background(), &out, "you/plugins", "", "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pins no plugins") {
		t.Errorf("printed:\n%s", out.String())
	}
}

// The wrapper's job is to find letsgo.mod and hand off to pruneStore.
func TestRunPluginPrune(t *testing.T) {
	noStore(t)
	t.Chdir(t.TempDir())
	write(t, "letsgo.mod", "build linux/amd64\n")

	if err := runPluginPrune(nil); err != nil {
		t.Fatal(err)
	}
}

func TestRunPluginInstallNeedsAName(t *testing.T) {
	err := runPluginInstall([]string{"@v0.2.0"})
	if err == nil || !strings.Contains(err.Error(), "no plugin name") {
		t.Errorf("err = %v", err)
	}
}

// The wrapper's job is to find letsgo.mod relative to the working directory,
// and to report it when there is none.
func TestRunPluginListWithoutAConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := runPluginList(nil); err == nil {
		t.Fatal("list without a letsgo.mod should be an error")
	}
}

func TestRunPluginListPrintsJSONWhenRequested(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("letsgo.mod", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		_ = runPluginList([]string{"--json"})
	})

	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runPluginList --json output = %q, want it to contain a schema field", out)
	}
}

// An unwritable destination has to fail before anything is reported installed.
func TestWriteExecutableReportsAnUnwritableDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "letsgo-multi")

	if err := writeExecutable(missing, []byte("bytes")); err == nil {
		t.Fatal("writing into a directory that does not exist should fail")
	}
}

// putInStore writes content into the store directly, as if it had been
// installed, and returns its digest.
func putInStore(t *testing.T, storeDir, name, content string) string {
	t.Helper()
	store, err := pluginstore.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, err := store.Put(digest, name, []byte(content)); err != nil {
		t.Fatal(err)
	}
	return digest
}

// Prune removes only what no pin in this repository references (CD-5).
func TestPruneStoreRemovesUnreferencedEntries(t *testing.T) {
	storeDir := noStore(t)
	t.Chdir(t.TempDir())

	kept := putInStore(t, storeDir, "letsgo-multi", "kept")
	putInStore(t, storeDir, "letsgo-env", "dropped")
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.1.0 "+kept+"\n")

	var out bytes.Buffer
	if err := pruneStore(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "letsgo-env") {
		t.Errorf("printed:\n%s\nwant the unreferenced entry named", out.String())
	}
	if strings.Contains(out.String(), "letsgo-multi") {
		t.Errorf("printed:\n%s\nwant the referenced entry left out", out.String())
	}

	store, err := pluginstore.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Lookup(kept, "letsgo-multi"); err != nil || !ok {
		t.Errorf("the referenced entry should survive: %v, %v", ok, err)
	}
	entries, err := store.Entries()
	if err != nil || len(entries) != 1 {
		t.Errorf("entries = %+v, err = %v", entries, err)
	}
}

func TestPruneStoreReportsNothingToPrune(t *testing.T) {
	storeDir := noStore(t)
	t.Chdir(t.TempDir())

	kept := putInStore(t, storeDir, "letsgo-multi", "kept")
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.1.0 "+kept+"\n")

	var out bytes.Buffer
	if err := pruneStore(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to prune") {
		t.Errorf("printed:\n%s", out.String())
	}
}

// letsgo plugin list names what prune would remove, so pruning is never a
// surprise.
func TestListPluginsReportsUnreferencedStoreEntries(t *testing.T) {
	storeDir := noStore(t)
	t.Chdir(t.TempDir())

	kept := putInStore(t, storeDir, "letsgo-multi", "kept")
	putInStore(t, storeDir, "letsgo-env", "dropped")
	write(t, "letsgo.mod", "build linux/amd64\n"+
		"plugin archive-layout letsgo-multi v0.1.0 "+kept+"\n")

	var out bytes.Buffer
	if err := listPlugins(&out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unreferenced by any pin here") {
		t.Errorf("printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "letsgo-env") {
		t.Errorf("printed:\n%s\nwant the unreferenced entry named", out.String())
	}
	if !strings.Contains(out.String(), "letsgo plugin prune") {
		t.Errorf("printed:\n%s\nwant the prune hint", out.String())
	}
}
