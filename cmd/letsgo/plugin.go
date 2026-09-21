package main

import (
	"context"
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
	"github.com/danielriddell21/letsgo/selfupdate"
)

// pluginRepo is where the plugins letsgo publishes come from. A flag can name
// another repository, because nothing about a plugin is special to this one —
// but the default is not a flag, so that the common case is a name rather than
// a URL somebody has to get right.
const pluginRepo = "danielriddell21/letsgo-plugins"

const pluginUsage = `letsgo plugin installs the external programs a release can call.

usage:
  letsgo plugin install <name>[@version]   download a plugin, verified, and print its pin
  letsgo plugin list                       the plugins this repository pins, and what is installed

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
func runPluginInstall(args []string) error {
	fs := flag.NewFlagSet("plugin install", flag.ExitOnError)
	dir := fs.String("o", "", "directory to install into (default: $GOBIN, or $GOPATH/bin)")
	repo := fs.String("repo", pluginRepo, "repository to install from, as owner/name")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("letsgo plugin install: expected one plugin, as letsgo-multi or letsgo-multi@v0.2.0")
	}

	name, requested := splitPluginRef(fs.Arg(0))
	if name == "" {
		return fmt.Errorf("letsgo plugin install: no plugin name in %q", fs.Arg(0))
	}

	ctx := context.Background()

	dest, err := installDir(ctx, *dir)
	if err != nil {
		return err
	}

	tokenValue, _ := plan.Token(*token)

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

	return installPlugin(ctx, os.Stdout, name, dest, options)
}

// installPlugin resolves the release, checks what it downloads and puts the
// executable in dest.
//
// Separated from the flags and from os.Stdout so that the behaviour worth
// asserting — that a verified binary lands where it was asked to, and that the
// pin printed afterwards names its digest — can be tested against a forge
// rather than against the network.
func installPlugin(ctx context.Context, w io.Writer, name, dest string, options selfupdate.Options) error {
	release, err := selfupdate.Check(ctx, options)
	if err != nil {
		return err
	}
	if release == nil {
		return fmt.Errorf("letsgo plugin install: %s has no releases", options.Repo)
	}

	binary, err := release.Download(ctx)
	if err != nil {
		return err
	}

	path := filepath.Join(dest, release.Binary)
	if err := writeExecutable(path, binary); err != nil {
		return err
	}

	fmt.Fprintf(w, "installed %s %s\n", name, release.Tag)
	fmt.Fprintf(w, "  %s\n", path)
	fmt.Fprintf(w, "  archive %s\n  binary  %s\n", short(release.SHA256), short(release.BinarySHA256))
	describePin(w, name, release)
	return nil
}

// describePin prints the line letsgo.mod wants, filled in as far as it can be
// filled in honestly.
//
// The hook comes from the repository's own config when the plugin is already
// declared there — the common case, which is an upgrade — and is left as a
// placeholder when it is not. letsgo does not guess it: a plugin documents the
// hook it answers, and a core carrying a table of plugin names would be a core
// that knows about particular plugins.
func describePin(w io.Writer, name string, release *selfupdate.Update) {
	hook := pinnedHook(name)
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
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return listPlugins(os.Stdout)
}

// listPlugins answers the question that follows every pin: is the program this
// file names actually here, and is it the one the file means?
//
// Takes a writer for the same reason the updater does: what it reports is the
// behaviour worth testing, and that is not checkable while it is tangled up
// with os.Stdout.
func listPlugins(w io.Writer) error {
	cfg, err := loadPluginConfig()
	if err != nil {
		return err
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
	return nil
}

// pluginStatus resolves one pin against what is on PATH.
func pluginStatus(p config.Plugin) (string, bool) {
	path, err := exec.LookPath(p.Command)
	if err != nil {
		return "not installed", false
	}

	digest, err := plugin.DigestOf(path)
	if err != nil {
		return "unreadable: " + path, false
	}
	if digest != p.Digest {
		return fmt.Sprintf("pinned %s, but %s is %s",
			shortDigest(p.Digest), path, shortDigest(digest)), false
	}
	return "ok  " + path, true
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
	goBin, err := gobuild.Toolchain()
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

func shortDigest(digest string) string {
	return short(strings.TrimPrefix(digest, "sha256:"))
}
