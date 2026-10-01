package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// pluginRepo is where the plugins letsgo publishes come from. A flag can name
// another repository, because nothing about a plugin is special to this one —
// but the default is not a flag, so that the common case is a name rather than
// a URL somebody has to get right.
const pluginRepo = "danielriddell21/letsgo-plugins"

// defaultPluginRepo is pluginRepo, unless the global config's `plugin-repo`
// directive names another one for this machine.
func defaultPluginRepo() string {
	return defaultPluginRepoWith(machineConfig())
}

// machineConfig is the global config, or an empty one when it cannot be
// read: a broken global file is plan's to report, not every command's.
func machineConfig() *config.Global {
	global, err := config.LoadGlobal()
	if err != nil {
		return &config.Global{}
	}
	return global
}

// defaultPluginRepoWith is defaultPluginRepo's core logic, taking the global
// config directly rather than loading it, so tests can exercise the
// `plugin-repo` directive without touching the machine's own config file.
func defaultPluginRepoWith(global *config.Global) string {
	if global.PluginRepo == "" {
		return pluginRepo
	}
	return global.PluginRepo
}

const pluginUsage = `letsgo plugin installs the external programs a release can call.

usage:
  letsgo plugin install                    install every plugin this repository pins
  letsgo plugin install <name>[@version]   download one plugin, verified, and print its pin
  letsgo plugin install --link             also put the plugin on PATH, for running it by hand
  letsgo plugin list [--json]              the plugins this repository pins, and what is installed
  letsgo plugin list --available           the plugins letsgo publishes, and what each answers
  letsgo plugin dir                        print the directory the plugins are installed in
  letsgo plugin prune                      remove store entries no pin in this repository references

run a subcommand with -h for its options.
`

func runPlugin(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, pluginUsage)
		os.Exit(2)
	}

	switch command := args[0]; command {
	case "install":
		return runPluginInstall(args[1:])
	case "list":
		return runPluginList(args[1:])
	case "dir":
		return runPluginDir(args[1:])
	case "prune":
		return runPluginPrune(args[1:])
	case "help", "-h", "--help":
		fmt.Print(pluginUsage)
	default:
		fmt.Fprintf(os.Stderr, "letsgo plugin: unknown subcommand %q\n\n%s", command, pluginUsage)
		os.Exit(2)
	}
	return nil
}

// runPluginInstall downloads a plugin and proves it is the one the release
// published, which is the whole reason this exists rather than a curl command
// in a README. The recipe it replaces fetched an archive over TLS and trusted
// it; this checks the archive against the release's own manifest, and the
// executable inside the archive against the manifest too.
//
// With no arguments it installs every plugin this repository pins in
// letsgo.mod — one command for a fresh clone, and one step in CI.
func runPluginInstall(args []string) error {
	fs := flag.NewFlagSet("plugin install", flag.ExitOnError)
	dir := fs.String("o", "", "with --link, directory to link into (default: $GOBIN, or $GOPATH/bin)")
	link := fs.Bool("link", false, "also put the plugin on PATH, for running it by hand")
	repo := fs.String("repo", defaultPluginRepo(), "repository to install from, as owner/name")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("letsgo plugin install: expected one plugin, as letsgo-multi or letsgo-multi@v0.2.0, " +
			"or no arguments to install every pin in " + plan.ConfigFile)
	}

	ctx := context.Background()
	tokenValue, _ := plan.Token(context.Background(), machineConfig(), *token)

	if fs.NArg() == 0 {
		return installAllPins(ctx, os.Stdout, *repo, tokenValue, *dir, *link)
	}

	name, requested := splitPluginRef(fs.Arg(0))
	if name == "" {
		return fmt.Errorf("letsgo plugin install: no plugin name in %q", fs.Arg(0))
	}

	// Current is left empty on purpose. This is not an update, and a plugin
	// that happens to be installed already has no say in which version the
	// repository asked for.
	options := selfupdate.Options{
		Repo:      *repo,
		Token:     tokenValue,
		UserAgent: "letsgo/" + version,
		Binary:    name,
	}
	if requested != "" && requested != "latest" {
		options.Tag = requested
	}

	return installPlugin(ctx, os.Stdout, name, options, *dir, *link)
}

// installAllPins installs every plugin the repository's own config pins, in
// the order they appear in the file.
func installAllPins(ctx context.Context, w io.Writer, repo, token, linkDir string, link bool) error {
	cfg, err := loadPluginConfig()
	if err != nil {
		return err
	}
	if len(cfg.Plugins) == 0 {
		fmt.Fprintf(w, "%s pins no plugins\n", plan.ConfigFile)
		return nil
	}

	for i, p := range cfg.Plugins {
		if i > 0 {
			fmt.Fprintln(w)
		}
		options := selfupdate.Options{
			Repo:      repo,
			Token:     token,
			UserAgent: "letsgo/" + version,
			Binary:    p.Command,
			Tag:       p.Version,
		}
		if err := installPlugin(ctx, w, p.Command, options, linkDir, link); err != nil {
			return fmt.Errorf("letsgo plugin install: %s: %w", p.Command, err)
		}
		// Installing proves the release is what it says it is, not that it is
		// what the config pins: a stale pin would otherwise report success and
		// fail at the next release.
		if err := verifyPinned(p); err != nil {
			return err
		}
	}
	return nil
}

// verifyPinned fails unless p now resolves to the binary its pin names.
func verifyPinned(p config.Plugin) error {
	if r := plugin.Resolve(p.Command, p.Digest, ".", machineConfig().PluginsDir); r.State != plugin.Installed {
		return fmt.Errorf("letsgo plugin install: %s %s installed, but it is not the digest the config pins (%s); update the pin",
			p.Command, p.Version, plugin.Short(p.Digest))
	}
	return nil
}

// installPlugin resolves the release, checks what it downloads, and writes
// the executable into the plugin store at its own digest — that is what
// plugin.Run looks up, and what lets two repositories pinning two different
// versions of the same plugin coexist. --link additionally puts a copy on
// PATH, for a plugin someone wants to run by hand.
//
// Separated from the flags and from os.Stdout so that the behaviour worth
// asserting — that a verified binary lands where it was asked to, and that the
// pin printed afterwards names its digest — can be tested against a forge
// rather than against the network.
func installPlugin(ctx context.Context, w io.Writer, name string, options selfupdate.Options, linkDir string, link bool) error {
	release, binary, path, err := fetchIntoStore(ctx, options)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "installed %s %s\n", name, release.Tag)
	fmt.Fprintf(w, "  %s\n", path)
	fmt.Fprintf(w, "  archive %s\n  binary  %s\n", short(release.SHA256), short(release.BinarySHA256))

	if link {
		dest, err := installDir(ctx, linkDir)
		if err != nil {
			return err
		}
		linked := filepath.Join(dest, release.Binary)
		if err := writeExecutable(linked, binary); err != nil {
			return err
		}
		fmt.Fprintf(w, "  linked  %s\n", linked)
	}

	describePin(w, name, release)
	return nil
}

// fetchIntoStore resolves the release options names, downloads and checks
// it, and writes the executable into the plugin store at its own digest. It is
// the part of an install that the language server's "update pin" shares.
func fetchIntoStore(ctx context.Context, options selfupdate.Options) (release *selfupdate.Update, binary []byte, path string, err error) {
	release, err = selfupdate.Check(ctx, options)
	if err != nil {
		return nil, nil, "", err
	}
	if release == nil {
		return nil, nil, "", fmt.Errorf("letsgo plugin install: %s has no releases", options.Repo)
	}

	binary, err = release.Download(ctx)
	if err != nil {
		return nil, nil, "", err
	}

	store, err := pluginstore.Open("", machineConfig().PluginsDir)
	if err != nil {
		return nil, nil, "", fmt.Errorf("letsgo plugin install: %w", err)
	}
	path, err = store.Put("sha256:"+release.BinarySHA256, release.Binary, binary)
	if err != nil {
		return nil, nil, "", fmt.Errorf("letsgo plugin install: %w", err)
	}
	return release, binary, path, nil
}

// describePin prints the line letsgo.mod wants, filled in as far as it can be
// filled in honestly.
//
// The hook comes from the repository's own config when the plugin is already
// declared there — the common case, which is an upgrade. Failing that, a
// plugin letsgo publishes itself has its hook looked up in the catalogue:
// unlike a third-party plugin, letsgo already knows what it answers, and
// making someone copy that out of a README would only invite a typo. A
// plugin that answers no hook at all is told so instead of being pinned. An
// unrecognised name is left as a placeholder, same as always: a plugin
// documents the hook it answers, and a core carrying a table of every
// plugin's hook would be a core that knows about particular plugins — every
// one but its own.
func describePin(w io.Writer, name string, release *selfupdate.Update) {
	hook, standalone := resolvePluginHook(name)
	if standalone {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %s does not answer a hook, so there is nothing to pin; run it directly.\n", name)
		return
	}
	if hook == "" {
		hook = "<hook>"
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "  pin it in letsgo.mod:")
	fmt.Fprintf(w, "    plugin %s %s v%s sha256:%s\n", hook, name, release.Version, release.BinarySHA256)

	if hook == "<hook>" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  the hook is the one the plugin documents that it answers; a plugin that")
		fmt.Fprintln(w, "  answers none reads a finished release and is not pinned at all.")
	}
}

// resolvePluginHook works out the hook to print a pin with: the repository's
// own config first, so upgrading a plugin never changes what it is pinned to
// even if the catalogue disagreed; failing that, the catalogue, for a plugin
// letsgo publishes. standalone reports a catalogued plugin that answers no
// hook.
func resolvePluginHook(name string) (hook string, standalone bool) {
	if hook := pinnedHook(name); hook != "" {
		return hook, false
	}
	if known, ok := plugin.Lookup(name); ok {
		return string(known.Hook), known.Hook == ""
	}
	return "", false
}

// pinnedHook reports the hook this repository already pins the plugin to, or
// "" when there is no config, the config does not parse, or the plugin is not
// in it. Every one of those is a normal state to be installing a plugin from,
// so none of them is an error here.
func pinnedHook(name string) string {
	cfg, err := loadPluginConfig()
	if err != nil {
		return ""
	}
	for _, p := range cfg.Plugins {
		if p.Command == name {
			return p.Hook
		}
	}
	return ""
}

func runPluginList(args []string) error {
	fs := flag.NewFlagSet("plugin list", flag.ExitOnError)
	available := fs.Bool("available", false, "list the plugins letsgo publishes, not what this repository pins")
	jsonOutput := fs.Bool("json", false, "print what this repository pins as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *available {
		return listAvailablePlugins(os.Stdout)
	}
	return listPlugins(os.Stdout, *jsonOutput)
}

// listAvailablePlugins prints the plugins letsgo publishes: the hook each
// answers, and what it does. Nothing here is pinned or installed by being
// listed — the point is to answer "is there already a plugin for this"
// before writing one.
func listAvailablePlugins(w io.Writer) error {
	for _, k := range plugin.Known {
		hook := string(k.Hook)
		if hook == "" {
			hook = "(standalone)"
		}
		fmt.Fprintf(w, "%-14s %-14s %s\n", hook, k.Command, k.Summary)
	}
	return nil
}

// pluginEntry is one pin resolved against the store/PATH, for `letsgo
// plugin list --json`.
type pluginEntry struct {
	Hook    string `json:"hook"`
	Command string `json:"command"`
	Version string `json:"version"`
	Digest  string `json:"digest,omitempty"`
	OK      bool   `json:"ok"`
	Status  string `json:"status"`
}

// jsonPluginsResult is the pin list's wire form for `letsgo plugin list
// --json`: schema-versioned (ED-13), so a consumer can tell which shape it's
// reading before the fields under it ever change.
type jsonPluginsResult struct {
	Schema  int           `json:"schema"`
	Plugins []pluginEntry `json:"plugins"`
}

// listPlugins answers the question that follows every pin: is the program this
// file names actually here, and is it the one the file means?
//
// Takes a writer for the same reason the updater does: what it reports is the
// behaviour worth testing, and that is not checkable while it is tangled up
// with os.Stdout.
func listPlugins(w io.Writer, jsonOutput bool) error {
	cfg, err := loadPluginConfig()
	if err != nil {
		return err
	}

	if jsonOutput {
		return printPluginsJSON(w, cfg)
	}

	if len(cfg.Plugins) == 0 {
		fmt.Fprintf(w, "%s pins no plugins\n", plan.ConfigFile)
		return nil
	}

	var unmet bool
	for _, p := range cfg.Plugins {
		status, ok := pluginStatus(p)
		if !ok {
			unmet = true
		}
		fmt.Fprintf(w, "%-14s %-14s %-9s %s\n", p.Hook, p.Command, p.Version, status)
	}

	if unmet {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  install a missing or mismatched plugin with")
		fmt.Fprintln(w, "  letsgo plugin install <name>@<version>")
	}

	reportUnreferencedStoreEntries(w, cfg)
	return nil
}

// printPluginsJSON is listPlugins' --json path: the same store/PATH
// resolution, minus the column-padded text formatting.
func printPluginsJSON(w io.Writer, cfg *config.Config) error {
	entries := make([]pluginEntry, 0, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		status, ok := pluginStatus(p)
		entries = append(entries, pluginEntry{
			Hook: p.Hook, Command: p.Command, Version: p.Version, Digest: p.Digest,
			OK: ok, Status: strings.TrimPrefix(status, "ok  "),
		})
	}

	data, err := json.MarshalIndent(jsonPluginsResult{Schema: 1, Plugins: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// reportUnreferencedStoreEntries names what letsgo plugin prune would remove,
// so pruning is never a surprise.
func reportUnreferencedStoreEntries(w io.Writer, cfg *config.Config) {
	store, err := pluginstore.Open("", machineConfig().PluginsDir)
	if err != nil {
		return
	}
	entries, err := store.Entries()
	if err != nil || len(entries) == 0 {
		return
	}

	referenced := pinsByDigestAndName(cfg)
	var unreferenced []pluginstore.Entry
	for _, e := range entries {
		if !referenced[digestAndName{e.Digest, e.Name}] {
			unreferenced = append(unreferenced, e)
		}
	}
	if len(unreferenced) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "  the store also holds, unreferenced by any pin here:")
	for _, e := range unreferenced {
		fmt.Fprintf(w, "  %-14s %s\n", e.Name, plugin.Short(e.Digest))
	}
	fmt.Fprintln(w, "  remove them with letsgo plugin prune")
}

// digestAndName identifies one store entry the same way its pin does: a
// plugin is only ever the same plugin when both agree.
type digestAndName struct {
	Digest, Name string
}

func pinsByDigestAndName(cfg *config.Config) map[digestAndName]bool {
	referenced := make(map[digestAndName]bool, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		referenced[digestAndName{p.Digest, p.Command}] = true
	}
	return referenced
}

// runPluginDir prints where the plugin store is, so a cache step need not
// know the platform's data directory.
func runPluginDir(args []string) error {
	fs := flag.NewFlagSet("plugin dir", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	store, err := pluginstore.OpenReadOnly("", machineConfig().PluginsDir)
	if err != nil {
		return err
	}
	fmt.Println(store.Dir())
	return nil
}

// runPluginPrune removes every store entry this repository's letsgo.mod does
// not pin.
func runPluginPrune(args []string) error {
	fs := flag.NewFlagSet("plugin prune", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return pruneStore(os.Stdout)
}

func pruneStore(w io.Writer) error {
	cfg, err := loadPluginConfig()
	if err != nil {
		return err
	}
	store, err := pluginstore.Open("", machineConfig().PluginsDir)
	if err != nil {
		return fmt.Errorf("letsgo plugin prune: %w", err)
	}

	referenced := pinsByDigestAndName(cfg)
	removed, err := store.Prune(func(digest, name string) bool {
		return referenced[digestAndName{digest, name}]
	})
	if err != nil {
		return fmt.Errorf("letsgo plugin prune: %w", err)
	}

	if len(removed) == 0 {
		fmt.Fprintln(w, "nothing to prune")
		return nil
	}
	for _, e := range removed {
		fmt.Fprintf(w, "removed %-14s %s\n", e.Name, plugin.Short(e.Digest))
	}
	return nil
}

// pluginStatus reports one pin as plugin.Run would resolve it, for a person
// to read. The working directory is the repository root, where letsgo.mod is
// read from.
func pluginStatus(p config.Plugin) (string, bool) {
	r := plugin.Resolve(p.Command, p.Digest, ".", machineConfig().PluginsDir)
	switch r.State {
	case plugin.Installed:
		return "ok  " + r.Path, true
	case plugin.Missing:
		return "not installed", false
	case plugin.Mismatch:
		return fmt.Sprintf("pinned %s, but %s is %s",
			plugin.Short(p.Digest), r.Path, plugin.Short(r.Digest)), false
	default:
		return "unreadable: " + r.Err.Error(), false
	}
}

func loadPluginConfig() (*config.Config, error) {
	data, err := os.ReadFile(plan.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("letsgo: reading %s: %w", plan.ConfigFile, err)
	}
	file, err := config.Parse(filepath.Base(plan.ConfigFile), data)
	if err != nil {
		return nil, err
	}
	return config.Decode(file)
}

// splitPluginRef splits "letsgo-multi@v0.2.0" into its name and version. A
// bare name means the latest release.
func splitPluginRef(ref string) (name, version string) {
	name, version, _ = strings.Cut(ref, "@")
	return name, version
}

// installDir is where a plugin goes: $GOBIN, then $GOPATH/bin.
//
// The same place go install puts things, because that is the directory a Go
// developer already has on PATH — and a plugin letsgo cannot find on PATH is a
// plugin that was not installed, however carefully it was downloaded.
func installDir(ctx context.Context, override string) (string, error) {
	dir := override
	if dir == "" {
		dir = goEnv(ctx, "GOBIN")
	}
	if dir == "" {
		if gopath := goEnv(ctx, "GOPATH"); gopath != "" {
			dir = filepath.Join(gopath, "bin")
		}
	}
	if dir == "" {
		return "", fmt.Errorf("letsgo plugin install: nowhere to install: set GOBIN or GOPATH, or pass -o")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("letsgo plugin install: %w", err)
	}
	return dir, nil
}

// goEnv asks the go command when the environment is silent: both of these have
// defaults that no environment variable carries, and the answer that matters
// is the one go itself would give.
//
// A silent failure is the right one here. Not knowing GOBIN is not an error;
// it only means the caller has to be told to pass -o.
func goEnv(ctx context.Context, name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	goBin, _, err := gobuild.Toolchain(machineConfig())
	if err != nil {
		return ""
	}
	out, err := exec.CommandContext(ctx, goBin, "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeExecutable installs the binary through a temporary file beside the
// target, so that an interrupted install leaves either the old plugin or none,
// never half of a new one. Beside it rather than in a temporary directory,
// because a rename across filesystems is a copy and stops being atomic.
func writeExecutable(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".letsgo-plugin-*")
	if err != nil {
		return fmt.Errorf("letsgo plugin install: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("letsgo plugin install: writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("letsgo plugin install: writing %s: %w", name, err)
	}
	if err := os.Chmod(name, 0o755); err != nil { //nolint:gosec // a plugin must be executable
		return fmt.Errorf("letsgo plugin install: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("letsgo plugin install: installing %s: %w", path, err)
	}
	return nil
}
