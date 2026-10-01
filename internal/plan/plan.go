// Package plan resolves a release without performing one.
//
// Everything that can be decided cheaply is decided here: configuration is
// read, defaults are inferred, the build matrix is validated, and the checks
// that do not need the network are run. The result is a complete description
// of what a release would produce.
//
// The ordering is the point. A release that is going to fail should fail in
// about two seconds, before a cross-compile matrix has been built, rather than
// after.
package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/install"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/internal/sumdb"
)

// ConfigFile is the optional configuration file letsgo reads.
const ConfigFile = "letsgo.mod"

// pluginConfigDir is where a plugin's own config lives, beside ConfigFile.
const pluginConfigDir = ".letsgo"

// disabledByConfig is the detail of a check its feature switched off.
const disabledByConfig = "disabled by config"

// Options control how a plan is resolved.
type Options struct {
	// Dir is any directory inside the module.
	Dir string

	// Global is the machine's configuration, read once by the caller and
	// handed down. Nil reads it from the file, which is what a command does.
	Global *config.Global

	// Snapshot builds an untagged working version.
	Snapshot bool

	// AllowDirty permits an unclean worktree. Never valid for a real release.
	AllowDirty bool

	// Publish adds the checks a release needs but a local build does not:
	// a forge to publish to, and a token permitted to write there.
	Publish bool

	// Token overrides the token read from the environment.
	Token string

	// TapToken overrides the token the Homebrew tap is written with. Empty
	// falls back to Token, which is what every repository did before the tap
	// could be reached by a credential of its own.
	TapToken string

	// ReleaseToken overrides the token the GitHub release itself is created
	// and published with. Empty falls back to Token, which is what every
	// repository did before the release could be published under the same
	// bot identity as the tap commit.
	ReleaseToken string

	// NewClient builds the forge client for a token, so the caller decides
	// where the forge is. Nil means the real one.
	NewClient func(token string) *github.Client

	// Analyse runs the gates that need program analysis rather than
	// inspection. They take seconds rather than milliseconds, so a bare plan
	// leaves them out and a release does not: the promise that a plan fails in
	// about two seconds is about configuration mistakes, and paying for an
	// analysis on every iteration would trade that away for little.
	Analyse bool

	// AllowVulnerable publishes despite reachable vulnerabilities, recording
	// which were accepted rather than hiding them.
	AllowVulnerable bool

	// AllowBreaking publishes an incompatible API change without a major
	// version bump.
	AllowBreaking bool

	// DisableProxyWarm skips priming the module proxy for this run only. It
	// means the same thing as `disable proxy-warm` in letsgo.mod, and is
	// recorded in the plan and the manifest the same way, so `--no-proxy-warm`
	// is indistinguishable from a repository that always disables it.
	DisableProxyWarm bool

	// Tag pins the version tag to resolve, overriding the "several tags on
	// HEAD" failure. Empty means the ordinary rule: whichever one tag is on
	// HEAD. `letsgo promote` is the one caller that needs this — it tags an
	// RC's commit with the stable version to rebuild there, and that commit
	// already carries the RC's own tag, so two qualifying tags on HEAD is
	// expected rather than a mistake.
	Tag string
}

// Status is the outcome of one check.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	Warn Status = "warn"
	Skip Status = "skip"
)

// Check is one gate result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`

	// Pos is where in the config file this check's directive was written,
	// when it was raised against one — nil for a check with no single line to
	// blame (a missing tool, a registry response). An editor turns it into a
	// diagnostic squiggle under that line.
	Pos *Pos `json:"pos,omitempty"`
}

// Pos names a location in a config file.
type Pos struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// Source records where a resolved value came from, for plan --explain.
type Source struct {
	Field string `json:"field"`
	Value string `json:"value"`
	From  string `json:"from"`
}

// Artifact is one archive a release would produce.
type Artifact struct {
	Name   string
	Target gobuild.Target
	Format archive.Format

	// Commands are the binaries inside it. One is the ordinary case; a module
	// whose product is a collection of tools ships them together.
	Commands []discover.MainPackage
}

// Group is one archive's contents, before targets multiply it.
//
// This is the seam a layout plugin decides: core knows how to carry several
// binaries in one archive, and what goes where is a question core answers by
// default and a plugin can answer instead.
type Group struct {
	// Name is the archive's base name, without version, platform or
	// extension.
	Name string

	Commands []discover.MainPackage

	// Targets and Tags are the group's own. The release's for a group that
	// declared none; a variant's where it did, because a variant exists
	// precisely to be compiled differently.
	Targets []gobuild.Target
	Tags    []string

	// Variant names the variant this group came from, empty for the default
	// build. It suffixes the archive.
	Variant string
}

// Program is the name the binary inside this group's archive carries.
//
// A variant's suffix tells two archives apart, which is all it is for.
// Carrying it into the binary would rename the program, and the command
// somebody types should not depend on which of two builds they installed.
func (g Group) Program() string {
	if g.Variant == "" {
		return g.Name
	}
	return strings.TrimSuffix(g.Name, "-"+g.Variant)
}

// resolvePlugins reads the pinned plugins, without running any.
//
// Whether a plugin is installed and matches its pin is checked when it runs;
// what is checked here is that the config names hooks letsgo has, because an
// unknown hook is a plugin that would silently never run.
func (p *Plan) resolvePlugins() {
	if len(p.Config.Plugins) == 0 {
		return
	}

	p.Plugins = make(map[plugin.Hook]plugin.Plugin, len(p.Config.Plugins))
	named := make([]string, 0, len(p.Config.Plugins))

	for _, configured := range p.Config.Plugins {
		hook := plugin.Hook(configured.Hook)
		if !hook.Valid() {
			hooks := make([]string, len(plugin.Hooks))
			for i, h := range plugin.Hooks {
				hooks[i] = string(h)
			}
			p.addAt(p.posOf(pluginDirective+configured.Hook), "plugins", Fail, "%q is not a hook; letsgo has %s",
				configured.Hook, strings.Join(hooks, " and "))
			return
		}
		if known, ok := plugin.Lookup(configured.Command); ok {
			switch {
			case known.Hook == "":
				p.addAt(p.posOf(pluginDirective+configured.Hook), "plugins", Fail,
					"%s does not answer a hook and cannot be pinned; run it directly after the release",
					configured.Command)
				return
			case known.Hook != hook:
				p.addAt(p.posOf(pluginDirective+configured.Hook), "plugins", Fail, "%s answers the %s hook, not %s",
					configured.Command, known.Hook, hook)
				return
			}
		}
		if relativePath(configured.Command) {
			p.addAt(p.posOf(pluginDirective+configured.Hook), "plugins", Warn,
				"%s is a path inside the repository, so a change to the checkout can change the program that runs; "+
					"its digest is what keeps that honest, so review any change to the pin closely",
				configured.Command)
		}
		p.Plugins[hook] = plugin.Plugin{
			Hook:    hook,
			Command: configured.Command,
			Version: configured.Version,
			Digest:  configured.Digest,
		}
		named = append(named, fmt.Sprintf("%s %s (%s)", configured.Command, configured.Version, hook))
	}

	p.note("plugins", strings.Join(named, ", "), ConfigFile)
}

// relativePath reports whether a plugin command names a file relative to the
// working directory rather than a program on PATH or at an absolute path.
func relativePath(command string) bool {
	rooted := filepath.IsAbs(command) || strings.HasPrefix(command, "/") || strings.HasPrefix(command, `\`)
	return !rooted && strings.ContainsAny(command, `/\`)
}

// checkPluginConfigFiles looks for a configured plugin's own config at the
// legacy root path and the current .letsgo/ path. Finding only the legacy
// one is a Warn suggesting the move; finding both is a Fail, since core has
// no way to tell which one the plugin would actually read.
func (p *Plan) checkPluginConfigFiles() {
	for _, configured := range p.Config.Plugins {
		legacy := filepath.Join(p.RootDir, configured.Command+".mod")
		modern := filepath.Join(p.RootDir, pluginConfigDir, plugin.ShortName(configured.Command)+".mod")

		_, legacyErr := os.Stat(legacy)
		_, modernErr := os.Stat(modern)
		hasLegacy, hasModern := legacyErr == nil, modernErr == nil

		switch {
		case hasLegacy && hasModern:
			p.addAt(p.posOf(pluginDirective+configured.Hook), "plugins", Fail,
				"%s has config at both %s and %s; remove the legacy file",
				configured.Command, legacy, modern)
		case hasLegacy:
			p.addAt(p.posOf(pluginDirective+configured.Hook), "plugins", Warn,
				"%s reads its config from %s; move it to %s",
				configured.Command, legacy, modern)
		}
	}
}

// machineSettings is the machine's global config and the programs resolved
// from it, settled once before anything shells out so that every later step
// is handed them rather than re-reading the machine.
type machineSettings struct {
	global    *config.Global
	globalErr error

	goBin, goSource   string
	gitBin, gitSource string
	gitErr            error
}

// resolveMachine settles the machine's configuration: the global config
// (read here only when the caller did not supply one) and the go and git
// commands it and the environment select.
func resolveMachine(global *config.Global) machineSettings {
	m := machineSettings{global: global}
	if m.global == nil {
		m.global, m.globalErr = config.LoadGlobal()
		if m.globalErr != nil {
			m.global = &config.Global{}
		}
	}
	if path, source, err := gobuild.Toolchain(m.global); err == nil {
		m.goBin, m.goSource = path, source
	}
	m.gitBin, m.gitSource, m.gitErr = discover.GitBinary(m.global)
	return m
}

// recordMachine records the machine settings on the plan, and where each
// machine-level value it can influence — the go toolchain, git, and the
// module proxy — actually came from, so `plan --explain` can name the source
// of each (CD-10). A malformed global file is a Fail here rather than a
// silent fallback: every other package that consults the global config falls
// back to its defaults on error, trusting this check to surface the problem
// once instead of nowhere.
func (p *Plan) recordMachine(m machineSettings) {
	if m.globalErr != nil {
		p.add("global config", Fail, "%v", m.globalErr)
	}
	p.Global = m.global
	p.GoBin, p.GitBin = m.goBin, m.gitBin

	if m.goBin != "" {
		p.note("go", m.goBin, m.goSource)
	}
	if m.gitErr == nil {
		p.note("git", m.gitBin, m.gitSource)
	}
	proxy, source := publish.ResolveProxy(os.Getenv("GOPROXY"), p.Global.Proxy, p.Global.Path)
	p.Proxy = proxy
	p.note("proxy", proxy, source)
}

// PluginsDir is the machine's `plugins` directive, or empty when the plan
// carries no global config (one assembled by hand rather than resolved).
func (p *Plan) PluginsDir() string {
	if p.Global == nil {
		return ""
	}
	return p.Global.PluginsDir
}

// hintPlugins nudges toward a first-party plugin whose job matches this
// release's shape, without failing anything: building eleven commands
// without letsgo-multi, or shipping a darwin variant beside a Homebrew tap
// without letsgo-cask, are both fine — just more work than they need to be.
func (p *Plan) hintPlugins() {
	if len(p.Commands) > 1 && !p.pluginPinned("letsgo-multi") {
		p.add("plugins", Warn,
			"this release builds %d commands into separate archives; letsgo-multi groups them into one "+
				"(see `letsgo plugin list --available`)", len(p.Commands))
	}
	if p.Config.BrewTap != "" && hasDarwinVariant(p.Config.Variants) && !p.pluginPinned("letsgo-cask") {
		p.add("plugins", Warn,
			"this release has a Homebrew tap and a darwin variant; letsgo-cask writes a cask for it "+
				"(see `letsgo plugin list --available`)")
	}
}

// pluginPinned reports whether the repository already pins the named
// command, by whichever hook.
func (p *Plan) pluginPinned(command string) bool {
	for _, configured := range p.Config.Plugins {
		if configured.Command == command {
			return true
		}
	}
	return false
}

// hasDarwinVariant reports whether any variant targets darwin. Variant
// targets are raw "goos/goarch" strings at this point: parsing them into
// gobuild.Target happens later, and a hint has no need to wait for that.
func hasDarwinVariant(variants []config.Variant) bool {
	for _, v := range variants {
		for _, t := range v.Targets {
			if strings.HasPrefix(t, "darwin/") {
				return true
			}
		}
	}
	return false
}

// applyLayoutPlugin asks the layout plugin which binaries share an archive.
//
// Core has already decided; the plugin replaces that answer, and what comes
// back is recorded in the manifest so that verification replays the layout
// rather than asking again. A machine with no plugins installed can still
// verify the release.
func (p *Plan) applyLayoutPlugin(ctx context.Context) {
	configured, ok := p.Plugins[plugin.HookArchiveLayout]
	if !ok {
		return
	}

	in := plugin.ArchiveLayoutInput{
		Project: p.Project,
		Version: p.Version,
		Module:  p.Module.Path,
		Targets: make([]string, len(p.Targets)),
	}
	for i, t := range p.Targets {
		in.Targets[i] = t.String()
	}
	for _, cmd := range p.Commands {
		in.Commands = append(in.Commands,
			plugin.InputCommand{Binary: cmd.BinaryName, Package: cmd.RelPath})
	}
	in.ConfigDir = filepath.Join(p.RootDir, pluginConfigDir)

	var out plugin.ArchiveLayoutOutput
	if err := plugin.Run(ctx, configured, p.RootDir, p.PluginsDir(), in, &out); err != nil {
		p.addAt(p.posOf(pluginDirective+string(configured.Hook)), "plugins", Fail, "%v", err)
		return
	}

	groups, err := layoutGroups(out, p.Commands)
	if err != nil {
		p.addAt(p.posOf(pluginDirective+string(configured.Hook)), "plugins", Fail, "plugin %s: %v", configured.Command, err)
		return
	}

	// A layout plugin decides which binaries share an archive, not how they
	// compile: the release's targets and tags still apply.
	for i := range groups {
		groups[i].Targets, groups[i].Tags = p.Targets, p.Tags
	}

	p.Groups = groups
	p.note("archives", describeGroups(groups), configured.Command)
	p.add("plugins", Pass, "%s laid out %d archive(s)", configured.Command, len(groups))
}

// applyLDFlagsPlugin asks the ldflags plugin for extra values to compile in.
//
// What comes back is appended to the artifact's recorded ldflags, which
// verification already replays exactly — so a value injected here is
// reproducible without the plugin, and without the environment it came from.
//
// That is also why it is worth saying out loud what has happened: the value is
// now in the binary and in the manifest, and neither is a place a secret can
// hide.
func (p *Plan) applyLDFlagsPlugin(ctx context.Context) {
	configured, ok := p.Plugins[plugin.HookLDFlags]
	if !ok {
		return
	}

	in := plugin.LDFlagsInput{
		Project: p.Project,
		Version: p.Version,
		Commit:  p.Git.ShortCommit,
		Date:    p.Git.CommitTime.UTC().Format(time.RFC3339),
		Module:  p.Module.Path,
		Targets: make([]string, len(p.Targets)),
	}
	for i, t := range p.Targets {
		in.Targets[i] = t.String()
	}
	in.ConfigDir = filepath.Join(p.RootDir, pluginConfigDir)

	var out plugin.LDFlagsOutput
	if err := plugin.Run(ctx, configured, p.RootDir, p.PluginsDir(), in, &out); err != nil {
		p.addAt(p.posOf(pluginDirective+string(configured.Hook)), "plugins", Fail, "%v", err)
		return
	}

	symbols, err := injectedSymbols(out.LDFlags)
	if err != nil {
		p.addAt(p.posOf(pluginDirective+string(configured.Hook)), "plugins", Fail, "plugin %s: %v", configured.Command, err)
		return
	}
	if len(symbols) == 0 {
		return
	}

	p.LDFlags = append(p.LDFlags, out.LDFlags...)
	p.note("injected values", strings.Join(symbols, ", "), configured.Command)
	p.addAt(p.posOf(pluginDirective+string(configured.Hook)), "plugins", Warn,
		"%s compiled %d value(s) into the binary: %s\n"+
			"  they are recoverable with `strings` and recorded in letsgo.json, so they are not secrets",
		configured.Command, len(symbols), strings.Join(symbols, ", "))
}

// injectedSymbols checks that a plugin returned only -X assignments, and names
// the symbols they write to.
//
// Only -X: the hook injects values, and a plugin that could pass arbitrary
// linker flags could change how the binary is linked rather than what is in
// it. Narrow is what makes the answer safe to record and replay.
func injectedSymbols(flags []string) ([]string, error) {
	var symbols []string

	for i := 0; i < len(flags); i++ {
		flag := flags[i]

		// Both spellings: `-X a.b=c` arrives as two arguments and `-X=a.b=c`
		// as one, and the linker accepts either.
		assignment, inline := strings.CutPrefix(flag, "-X=")
		if !inline {
			if flag != "-X" {
				return nil, fmt.Errorf(
					"it returned %q; the ldflags hook may only return -X assignments", flag)
			}
			if i+1 >= len(flags) {
				return nil, fmt.Errorf("it returned a trailing -X with nothing to assign")
			}
			i++
			assignment = flags[i]
		}

		symbol, err := injectedSymbol(assignment)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, symbol)
	}
	return symbols, nil
}

// injectedSymbol checks one -X assignment and names the symbol it writes to.
func injectedSymbol(assignment string) (string, error) {
	symbol, _, ok := strings.Cut(assignment, "=")
	if !ok || symbol == "" {
		return "", fmt.Errorf("it returned -X %q, which assigns nothing", assignment)
	}
	if _, name, ok := cutSymbol(symbol); !ok || name == "" {
		return "", fmt.Errorf(
			"it returned -X %q; the symbol must name a package and a variable", assignment)
	}

	// The manifest records the linker flags as one space-joined string, and
	// verification splits that back into fields. A value containing whitespace
	// would not survive the round trip: the rebuild would use different flags
	// and report an unreproducible binary, with nothing pointing at the real
	// cause.
	if strings.ContainsAny(assignment, " \t\n") {
		return "", fmt.Errorf(
			"it returned a value for %s containing whitespace, which cannot be recorded "+
				"and replayed", symbol)
	}
	return symbol, nil
}

// layoutGroups turns a plugin's answer into groups, refusing one that does not
// account for every command exactly once.
//
// Checked rather than trusted: a layout that drops a command ships a release
// missing a binary, and one that repeats a command produces two archives
// claiming the same program. Both are silent.
func layoutGroups(out plugin.ArchiveLayoutOutput, commands []discover.MainPackage) ([]Group, error) {
	byName := make(map[string]discover.MainPackage, len(commands))
	for _, cmd := range commands {
		byName[cmd.BinaryName] = cmd
	}

	seen := map[string]string{}
	groups := make([]Group, 0, len(out.Archives))

	for _, archive := range out.Archives {
		group, err := layoutGroup(archive, byName, seen)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}

	for _, cmd := range commands {
		if _, ok := seen[cmd.BinaryName]; !ok {
			return nil, fmt.Errorf("%s is in no archive, so the release would not ship it", cmd.BinaryName)
		}
	}
	return groups, nil
}

// layoutGroup turns one of a plugin's archives into a group, recording in seen
// which archive claimed each binary so that a second claim can be named
// against the first.
func layoutGroup(
	archive plugin.OutputArchive,
	byName map[string]discover.MainPackage,
	seen map[string]string,
) (Group, error) {
	if archive.Name == "" {
		return Group{}, fmt.Errorf("it returned an archive with no name")
	}
	if len(archive.Binaries) == 0 {
		return Group{}, fmt.Errorf("archive %s holds no binaries", archive.Name)
	}

	group := Group{Name: archive.Name}
	for _, binary := range archive.Binaries {
		cmd, ok := byName[binary]
		if !ok {
			return Group{}, fmt.Errorf("archive %s names %s, which this module does not build",
				archive.Name, binary)
		}
		if first, repeated := seen[binary]; repeated {
			return Group{}, fmt.Errorf("%s is in both %s and %s", binary, first, archive.Name)
		}
		seen[binary] = archive.Name
		group.Commands = append(group.Commands, cmd)
	}
	return group, nil
}

func describeGroups(groups []Group) string {
	out := make([]string, len(groups))
	for i, g := range groups {
		binaries := make([]string, len(g.Commands))
		for j, cmd := range g.Commands {
			binaries[j] = cmd.BinaryName
		}
		out[i] = fmt.Sprintf("%s (%s)", g.Name, strings.Join(binaries, ", "))
	}
	return strings.Join(out, ", ")
}

// ImageTarget is where a release's container images go.
type ImageTarget struct {
	// Registry is the host as it is written and published — "docker.io", not
	// the "registry-1.docker.io" its API answers on. APIHost is the latter.
	// Recording the written form matters: it is what goes in the manifest and
	// what someone types into `docker pull`.
	Registry string
	APIHost  string

	// Repository is the path under the registry. A module with one command
	// publishes to Repository; one with several publishes each command to
	// Repository/<binary>, because a repository holds one image.
	Repository string

	// Base is the image to stack on. The zero value means scratch.
	Base oci.Reference

	// Cmd is the default argument list, and Expose the ports to record. Both
	// are config verbatim: they are strings in the image config, so the commit
	// pins them exactly as it pins the reference.
	Cmd    []string
	Expose []string

	// Platforms are the targets that get an image, which is the Linux subset
	// of the build matrix: nothing else runs in a container.
	Platforms []gobuild.Target
}

// Repos returns the repository each binary publishes to.
func (t *ImageTarget) Repos(binaries []string) map[string]string {
	out := make(map[string]string, len(binaries))
	for _, binary := range binaries {
		if len(binaries) == 1 {
			out[binary] = t.Repository
			continue
		}
		out[binary] = t.Repository + "/" + binary
	}
	return out
}

// Plan is a fully resolved, unexecuted release.
type Plan struct {
	// Module is the module that gets built, which the `module` directive can
	// move into a subdirectory. RootDir is the repository itself: where git
	// runs, where letsgo.mod lives, what the source archive covers, and what
	// archive files resolve against.
	//
	// They are the same directory in every repository that does not say
	// otherwise, and the distinction only exists because a nested module is a
	// fact about the layout rather than a different project: a release of
	// ./web still ships the repository's README, and still has to publish
	// source that can rebuild it.
	Module  discover.Module
	RootDir string

	// root is the module beside letsgo.mod, kept for the names that describe
	// the repository rather than the module being built.
	root discover.Module

	Git   discover.Git
	Scope discover.Scope

	// GitBin is the git command resolved from the environment and global
	// config, handed to everything that reads the repository.
	GitBin string

	Repo       discover.Repo
	HasRepo    bool
	Config     *config.Config
	ConfigPath string

	// Features are the departures from the defaults this release resolved,
	// from letsgo.mod's `disable` directive and any one-run flag that means
	// the same thing.
	Features feature.Set

	// Required are the features whose Skip this release turns into a Fail,
	// from letsgo.mod's `require` directive.
	Required []string

	Project  string
	Version  string
	Tag      string
	Snapshot bool

	Targets   []gobuild.Target
	Commands  []discover.MainPackage
	LDFlags   []string
	Files     []string
	Artifacts []Artifact

	// Groups are the archives the release produces, each holding one or more
	// commands.
	Groups []Group

	// Plugins are the pinned programs this release runs, by hook.
	Plugins map[plugin.Hook]plugin.Plugin

	// Tags are build tags passed to the compiler.
	Tags []string

	// Symbols names the variables the version metadata is injected into,
	// fully qualified. It defaults to the conventional main.version,
	// main.commit and main.date.
	Symbols VersionSymbols

	// Budgets caps each target's binary size. Parsed here so that a malformed
	// size is reported with every other planning problem, rather than after a
	// full matrix has been compiled.
	Budgets map[string]bytesize.Size

	// Tap is the Homebrew repository a formula is published to. Zero when no
	// tap is configured.
	Tap github.Repo

	// Image is the container image a release publishes. Nil when none is
	// configured, which is the default.
	Image *ImageTarget

	// Global is the machine configuration this plan was resolved under, so
	// what is done with the plan reads the same settings the plan reported.
	Global *config.Global

	// GoBin is the go command resolved from the environment and global
	// config, empty when none was found.
	GoBin string

	// Proxy is the module proxy this release would warm, resolved from
	// GOPROXY, the global config, or the fixed default.
	Proxy string

	Checks  []Check
	Sources []Source

	// APIChanges is the exported API delta against the previous release. It
	// feeds the changelog as well as the gate: the diff describes what the
	// code did, where a commit message describes what someone meant.
	APIChanges []gate.Change
}

// OK reports whether every check passed.
func (p *Plan) OK() bool {
	for _, c := range p.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

func (p *Plan) add(name string, status Status, format string, args ...any) {
	p.Checks = append(p.Checks, Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
}

// addAt is add for a check raised against a specific line in the config
// file: an editor places it as a diagnostic there instead of only listing it
// in the plan report.
func (p *Plan) addAt(pos *Pos, name string, status Status, format string, args ...any) {
	p.Checks = append(p.Checks,
		Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...), Pos: pos})
}

// configPos turns a directive's position within letsgo.mod into a Check's
// Pos, nil when there is nothing to point at (no config file, or a
// directive that recorded no position for this value).
// posOf is configPos for the directive written under key (see config.Config.Pos).
func (p *Plan) posOf(key string) *Pos {
	return p.configPos(p.Config.Pos[key])
}

func (p *Plan) configPos(cp config.Position) *Pos {
	if cp.Line == 0 || p.ConfigPath == "" {
		return nil
	}
	return &Pos{File: p.ConfigPath, Line: cp.Line, Col: cp.Col}
}

func (p *Plan) note(field, value, from string) {
	p.Sources = append(p.Sources, Source{Field: field, Value: value, From: from})
}

// tokenNoteFrom describes where a resolved token's source string itself came
// from, for plan --explain: a flag, token-command, or (everything else) an
// environment variable.
func tokenNoteFrom(source string) string {
	switch {
	case strings.HasPrefix(source, "--"):
		return "flag"
	case source == tokenCommandSource:
		return tokenCommandSource
	default:
		return "environment"
	}
}

// Resolve builds a plan. It returns an error only when the repository cannot
// be inspected at all; ordinary problems become failed checks, so that one run
// reports every issue rather than only the first.
func Resolve(ctx context.Context, opts Options) (*Plan, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}

	root, err := discover.FindModule(dir)
	if err != nil {
		return nil, err
	}

	machine := resolveMachine(opts.Global)
	if machine.gitErr != nil {
		return nil, machine.gitErr
	}

	git, err := discover.FindGit(ctx, machine.gitBin, root.Dir)
	if err != nil {
		return nil, err
	}

	scope, err := discover.NewScope(git.TopLevel, root.Dir)
	if err != nil {
		return nil, err
	}

	p := &Plan{Module: root, RootDir: root.Dir, root: root, Git: git, Scope: scope, Snapshot: opts.Snapshot}
	p.note("commit", git.Commit, "git HEAD")
	p.note("commit time", git.CommitTime.Format("2006-01-02T15:04:05Z"), "git committer timestamp")
	if scope.Prefix != "" {
		p.note("scope", scope.Dir, "the module's own directory")
	}

	if repo, err := discover.FindRepo(ctx, machine.gitBin, root.Dir); err == nil {
		p.Repo, p.HasRepo = repo, true
		p.note("repository", repo.String(), "git remote origin")
	}

	p.recordMachine(machine)

	// The config is read before the module is settled, because it is what can
	// move it: `module web` says the go.mod to build is not the one beside
	// letsgo.mod.
	p.loadConfig(root.Dir)
	p.resolveFeatures(opts)
	p.resolvePlugins()
	p.checkPluginConfigFiles()
	p.resolveModule()
	p.checkReplace()
	p.resolveProject()
	p.resolveVersion(ctx, opts)
	p.checkWorktree(opts)
	p.resolveTargets(ctx)
	p.resolveBudgets()
	p.resolveCommands(ctx)
	p.resolveFiles(ctx)
	p.resolveArtifacts(ctx)
	p.checkInstallScriptRequired()
	p.checkNotesRequired()

	p.resolveTap()
	p.resolveImage(ctx)
	p.hintPlugins()

	if opts.Publish {
		p.checkForge(ctx, opts)
		p.checkSumdb()
	}
	if opts.Analyse {
		p.checkVulnerabilities(ctx, opts)
		p.checkAPICompatibility(ctx, opts)
	}

	return p, nil
}

// TokenEnvVars are the environment variables consulted for a forge token, in
// order. GH_TOKEN is what the GitHub CLI sets, so a machine already set up to
// use gh needs no further configuration.
var TokenEnvVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// TapTokenEnvVars are the environment variables consulted for the token the
// Homebrew tap is written with.
//
// Separate from TokenEnvVars because the two credentials want different
// scopes: the release is published to this repository, which the workflow
// token already covers, and the tap lives in another one, which needs an App
// token. Keeping them apart means the App need not be installed here at all.
var TapTokenEnvVars = []string{"LETSGO_TAP_TOKEN"}

// ReleaseTokenEnvVars are the environment variables consulted for the token
// the GitHub release is published with.
//
// Separate from TokenEnvVars for the same reason TapTokenEnvVars is: a
// repository that wants the release itself attributed to a bot identity,
// distinct from whatever token ran the workflow, needs its own credential to
// name.
var ReleaseTokenEnvVars = []string{"LETSGO_RELEASE_TOKEN"}

// tokenCommandSource is the source Token reports when the value came from
// the global config's token-command, rather than a flag or the environment.
const tokenCommandSource = "token-command"

// Token returns the resolved token, and where it came from.
func Token(ctx context.Context, override string) (token, source string) {
	if override != "" {
		return override, "--token"
	}
	for _, name := range TokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	global, err := config.LoadGlobal()
	if err != nil {
		global = &config.Global{}
	}
	return tokenWith(ctx, global)
}

// tokenWith is Token's last fallback tier, taking the global config directly
// rather than loading it, so tests can exercise token-command without
// relying on config.LoadGlobal's process-wide memoization.
func tokenWith(ctx context.Context, global *config.Global) (token, source string) {
	if token := runTokenCommand(ctx, global.TokenCommand); token != "" {
		return token, tokenCommandSource
	}
	return "", ""
}

// runTokenCommand runs the global config's token-command and returns its
// output as the token. A missing command, or one that exits non-zero, yields
// no token, with its stderr shown — a broken credential helper must not stop
// the flag/environment fallback chain from reaching "no token" (the PBS edge
// case for token-command).
//
// The token is returned and nothing else: it is never logged, and only the
// argv that produced it is ever recorded, as tokenCommandSource.
func runTokenCommand(ctx context.Context, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			fmt.Fprintf(os.Stderr, "token-command: %v: %s\n", err, detail)
		} else {
			fmt.Fprintf(os.Stderr, "token-command: %v\n", err)
		}
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TapToken returns the token the Homebrew tap is written with, and where it
// came from.
//
// It falls back to the release token so that a repository which has always
// published with one credential keeps working unchanged. What the fallback
// costs is stated where it is configured, not here: one token that can write
// to both repositories is one token whose loss reaches both.
func TapToken(ctx context.Context, override, tokenOverride string) (token, source string) {
	if override != "" {
		return override, "--tap-token"
	}
	for _, name := range TapTokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return Token(ctx, tokenOverride)
}

// ReleaseToken returns the token the GitHub release is published with, and
// where it came from.
//
// It falls back to the release token so that a repository which has always
// published with one credential keeps working unchanged, exactly as
// TapToken does for the tap.
func ReleaseToken(ctx context.Context, override, tokenOverride string) (token, source string) {
	if override != "" {
		return override, "--release-token"
	}
	for _, name := range ReleaseTokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return Token(ctx, tokenOverride)
}

// checkVulnerabilities refuses to publish a binary that can reach known
// vulnerable code.
//
// The claim is deliberately narrow. Not "this release has no vulnerable
// dependencies", which is unachievable and would block every release, but
// "nothing in this binary can execute code with a known advisory against it".
// That is checkable, actionable, and rare enough to be worth stopping for.
func (p *Plan) checkVulnerabilities(ctx context.Context, opts Options) {
	if !p.Features.On("vulncheck") {
		p.add("vulnerabilities", Skip, disabledByConfig)
		return
	}

	found, err := gate.Vulncheck(ctx, p.Module.Dir)

	switch {
	case errors.Is(err, gate.ErrToolMissing):
		// A gate that did not run is not a gate that passed.
		p.skip("vulnerabilities", "vulncheck", "%v", err)
		return
	case err != nil:
		p.add("vulnerabilities", Warn, "could not be checked: %v", err)
		return
	case len(found) == 0:
		p.add("vulnerabilities", Pass, "no reachable vulnerabilities")
		return
	}

	lines := make([]string, 0, len(found)+1)
	for _, v := range found {
		lines = append(lines, v.String())
	}

	if opts.AllowVulnerable {
		// Recorded rather than suppressed: the manifest carries gate results,
		// so a consumer can see what this release was published in spite of.
		p.add("vulnerabilities", Warn, "%s\naccepted with --allow-vulnerable", strings.Join(lines, "\n"))
		return
	}

	lines = append(lines, "override with --allow-vulnerable")
	p.add("vulnerabilities", Fail, "%s", strings.Join(lines, "\n"))
}

// apiCompatibility is the check name every step of checkAPICompatibility
// reports under. apiGate is the feature that check is gated and required by.
const (
	apiCompatibility = "api compatibility"
	apiGate          = "api-gate"
)

// checkAPICompatibility refuses a release whose version promises more
// compatibility than its API delivers.
//
// Within a major version, removing or changing an exported symbol breaks every
// dependant at compile time. Go's answer is a new major version with a new
// import path, and nothing enforces it, so the mistake is made quietly and
// found by other people.
func (p *Plan) checkAPICompatibility(ctx context.Context, opts Options) {
	if !p.Features.On(apiGate) {
		p.add(apiCompatibility, Skip, disabledByConfig)
		return
	}
	// Nothing importable is a fact about the module, not a gap in the release,
	// so it stays a Skip even when the gate is required.
	if importable, err := gate.Importable(ctx, p.GoBin, p.Module.Dir); err == nil && !importable {
		p.add(apiCompatibility, Skip, "%v", gate.ErrNothingExported)
		return
	}
	if p.Tag == "" {
		p.skip(apiCompatibility, apiGate, "not a tagged release")
		return
	}

	tags, err := discover.Tags(ctx, p.GitBin, p.RootDir, p.Scope.Prefix)
	if err != nil {
		p.skip(apiCompatibility, apiGate, "no earlier release to compare against")
		return
	}
	previous, ok := p.Scope.PreviousTag(tags, p.Tag)
	if !ok {
		p.skip(apiCompatibility, apiGate, "no earlier release to compare against")
		return
	}

	old, cleanup, err := checkoutTag(ctx, p.GitBin, p.RootDir, previous, p.Scope.Dir)
	if err != nil {
		p.add(apiCompatibility, Warn, "could not check out %s: %v", previous, err)
		return
	}
	defer cleanup()

	changes, err := gate.APIDiff(ctx, p.GoBin, old, p.Module.Dir)
	switch {
	case errors.Is(err, gate.ErrToolMissing), errors.Is(err, gate.ErrNothingExported):
		p.skip(apiCompatibility, apiGate, "%v", err)
		return
	case err != nil:
		p.add(apiCompatibility, Warn, "could not be checked: %v", err)
		return
	}
	p.APIChanges = changes

	breaking := gate.Incompatibles(changes)
	if len(breaking) == 0 {
		p.add(apiCompatibility, Pass, "the exported API is backward compatible with %s", previous)
		return
	}

	// A major bump is exactly what an incompatible change calls for, so
	// making one is the correct outcome rather than a problem.
	if bumpBetween(previous, p.Tag) == "major" {
		p.add(apiCompatibility, Pass, "%d incompatible change(s), and %s is a major release",
			len(breaking), p.Tag)
		return
	}

	if opts.AllowBreaking {
		p.add(apiCompatibility, Warn, "%s\naccepted with --allow-breaking", describe(breaking))
		return
	}

	p.add(apiCompatibility, Fail,
		"%s is not a major release, but the API is not backward compatible with %s\n%s\n%s",
		p.Tag, previous, describe(breaking),
		"a breaking change needs a major version and a matching /vN module path\noverride with --allow-breaking")
}

func describe(changes []gate.Change) string {
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, "  "+c.String())
	}
	return strings.Join(lines, "\n")
}

// bumpBetween reports how two versions differ.
func bumpBetween(previous, current string) string {
	from, okFrom := semver.Parse(previous)
	to, okTo := semver.Parse(current)
	if !okFrom || !okTo {
		return "unknown"
	}
	switch {
	case to.Major != from.Major:
		return "major"
	case to.Minor != from.Minor:
		return "minor"
	default:
		return "patch"
	}
}

// checkoutTag checks out tag into a scratch worktree and returns the
// module's own directory within it — relDir, slash-separated and relative to
// the repository, exactly as Scope.Dir names it. A worktree always holds the
// whole repository, so a module nested in it is compared at <worktree>/relDir,
// never at the worktree's own root.
func checkoutTag(ctx context.Context, gitBin, repoDir, tag, relDir string) (dir string, cleanup func(), err error) {
	base, err := os.MkdirTemp("", "letsgo-apidiff-")
	if err != nil {
		return "", nil, fmt.Errorf("plan: scratch directory: %w", err)
	}

	worktree := filepath.Join(base, "old")
	if err := discover.AddWorktree(ctx, gitBin, repoDir, worktree, tag); err != nil {
		_ = os.RemoveAll(base)
		return "", nil, err
	}

	return filepath.Join(worktree, filepath.FromSlash(relDir)), func() {
		_ = discover.RemoveWorktree(ctx, gitBin, repoDir, worktree)
		_ = os.RemoveAll(base)
	}, nil
}

func underActions() bool { return os.Getenv("GITHUB_ACTIONS") == "true" }

// unconfirmedUnderActions says what could not be established and where to
// look if the publish then fails, without asserting a permission is missing.
func unconfirmedUnderActions(repo string) string {
	return fmt.Sprintf(
		"write access to %s could not be confirmed\n"+
			"a workflow token's permissions are not described by the repository\n"+
			"endpoint, so this is not evidence that it lacks them\n"+
			"if publishing fails: check `permissions: contents: write` in the\n"+
			"workflow, and Settings \u2192 Actions \u2192 General \u2192 Workflow permissions",
		repo)
}

// noActionsWriteAccess explains a refusal in terms of where the permission is
// granted to a workflow, which is not where a token's scopes live.
//
// A workflow may request no more than the repository allows, so
// `permissions: contents: write` has no effect while the repository default is
// read-only — and that setting is several screens away from the workflow file
// the reader is looking at.
func noActionsWriteAccess(repo string) string {
	return fmt.Sprintf(
		"this workflow's token may not create releases in %s\n"+
			"check both: `permissions: contents: write` in the workflow, and\n"+
			"Settings \u2192 Actions \u2192 General \u2192 Workflow permissions\n"+
			"\u2192 \"Read and write permissions\"",
		repo)
}

// noWriteAccess explains a missing permission in terms of where it is granted.
// Only reached outside Actions, where the reported permission is trustworthy.
func noWriteAccess(source, repo string) string {
	return fmt.Sprintf(
		"%s cannot write to %s; a release needs contents:write\n"+
			"a fine-grained token needs the Contents repository permission set to\n"+
			"Read and write; a classic token needs the repo scope",
		source, repo)
}

// checkForge verifies, before anything is built, that there is somewhere to
// publish and permission to do it. One API call now is worth more than a
// perfect set of artifacts and a 401.
func (p *Plan) checkForge(ctx context.Context, opts Options) {
	if !p.HasRepo {
		p.add("forge", Fail, "no 'origin' remote, so there is nowhere to publish")
		return
	}
	if p.Repo.Host != "github.com" {
		p.add("forge", Fail, "%s is not supported yet; letsgo publishes to github.com", p.Repo.Host)
		return
	}

	token, source := Token(ctx, opts.Token)
	if token == "" {
		p.add("token", Fail, "no token; set %s", strings.Join(TokenEnvVars, " or "))
		return
	}

	client := opts.client(token)
	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}
	access, err := client.CheckAccess(ctx, repo)
	if err != nil {
		// Unreachable is definitive: the repository is private to this token,
		// renamed, or gone.
		p.add("token", Fail, "%v", err)
		return
	}
	if access.Archived {
		p.add("token", Fail, "%s is archived and cannot receive a release", p.Repo)
		return
	}
	p.note("token", source, tokenNoteFrom(source))

	if !p.checkRelease(ctx, opts, repo, token, client, access) {
		return
	}

	// The tap is probed with the credential that will actually write to it.
	// Probing the release's credential instead is how a plan passes and the
	// release then fails on its last step, which is the one failure this
	// gate exists to prevent.
	tapToken, tapSource := TapToken(ctx, opts.TapToken, opts.Token)
	tapClient := client
	if tapToken != token {
		tapClient = opts.client(tapToken)
		p.note("tap token", tapSource, tokenNoteFrom(tapSource))
	}

	p.checkTap(ctx, tapClient, tapSource)
}

// client builds the forge client for token.
func (o Options) client(token string) *github.Client {
	if o.NewClient != nil {
		return o.NewClient(token)
	}
	return github.New(token)
}

// ClientAt returns a client factory for a forge served at endpoint, for the
// callers that stand one up in place of the real one.
func ClientAt(endpoint string) func(token string) *github.Client {
	return func(token string) *github.Client {
		c := github.New(token)
		c.SetEndpoints(endpoint, endpoint)
		return c
	}
}

// checkRelease establishes that the release can be created with whichever
// credential will actually create it — its own, if one is configured,
// otherwise the plain token — the same way checkTap establishes it for the
// formula. Probing the wrong one is how a plan passes and the release then
// fails on its last step, which is the one failure this gate exists to
// prevent.
//
// client and access are what checkForge already established for the plain
// token, reused when no release token is configured so the common case costs
// no second API call.
func (p *Plan) checkRelease(
	ctx context.Context, opts Options, repo github.Repo, token string, client *github.Client, access github.Access,
) bool {
	releaseToken, releaseSource := ReleaseToken(ctx, opts.ReleaseToken, opts.Token)
	releaseClient, releaseAccess := client, access
	if releaseToken != token {
		releaseClient = opts.client(releaseToken)
		var err error
		releaseAccess, err = releaseClient.CheckAccess(ctx, repo)
		if err != nil {
			p.add("token", Fail, "%v", err)
			return false
		}
		if releaseAccess.Archived {
			p.add("token", Fail, "%s is archived and cannot receive a release", p.Repo)
			return false
		}
		p.note("release token", releaseSource, tokenNoteFrom(releaseSource))
	}

	switch {
	case releaseAccess.CanPush:
		p.add("token", Pass, "%s can write to %s", releaseSource, p.Repo)
		return true

	case !underActions():
		// For a user token the reported permission is accurate, so this is a
		// real answer and worth stopping for.
		p.add("token", Fail, "%s", noWriteAccess(releaseSource, p.Repo.String()))
		return false

	default:
		// A workflow token is an installation token, whose permissions the
		// repository endpoint does not describe. Ask the forge directly.
		allowed, err := releaseClient.CanCreateRelease(ctx, repo)
		switch {
		case err != nil:
			// Neither established nor refuted. Blocking here would refuse
			// correctly configured releases on no evidence, and the publish
			// attempt will give a definitive answer shortly.
			p.add("token", Warn, "%s", unconfirmedUnderActions(p.Repo.String()))
			return true
		case allowed:
			p.add("token", Pass, "%s may create releases in %s", releaseSource, p.Repo)
			return true
		default:
			p.add("token", Fail, "%s", noActionsWriteAccess(p.Repo.String()))
			return false
		}
	}
}

// resolveTap parses the configured Homebrew tap.
//
// Separate from checkTap so that a malformed tap is reported by a plain
// `letsgo plan`, which contacts nothing.
func (p *Plan) resolveTap() {
	if p.Config.BrewTap == "" {
		return
	}
	tap, err := brew.ParseTap(p.Config.BrewTap)
	if err != nil {
		p.addAt(p.posOf("brew"), brewTap, Fail, "%v", err)
		return
	}
	p.Tap = tap
	p.note(brewTap, tap.String(), ConfigFile)
}

// resolveImage works out where the container images go.
//
// The reference names a repository and nothing else: the tag comes from the
// release, so accepting one here would create two answers to what a release is
// called and let them disagree.
func (p *Plan) resolveImage(ctx context.Context) {
	if p.Config.Image == nil {
		return
	}

	reference, source := p.Config.Image.Reference, ConfigFile
	if reference == "" {
		if !p.HasRepo {
			p.addAt(p.posOf("image"), "image", Fail,
				"no 'origin' remote, so there is no default image name; write one after `image`")
			return
		}
		// ghcr.io mirrors the repository it is released from, which is the
		// one name nobody has to be told.
		reference = "ghcr.io/" + p.Repo.Owner + "/" + p.Project
		source = "the repository owner and project name"
	}

	ref, err := oci.ParseReference(reference)
	if err != nil {
		p.addAt(p.posOf("image"), "image", Fail, "%v", err)
		return
	}
	if ref.Tag != "" || ref.Digest != "" {
		p.addAt(p.posOf("image"), "image", Fail,
			"%s carries a tag; the tag comes from the release, so name the repository only", reference)
		return
	}

	target := &ImageTarget{
		Registry: ref.Registry, APIHost: ref.APIHost(), Repository: ref.Repository,
		Cmd: p.Config.Image.Cmd, Expose: p.Config.Image.Expose,
	}
	for _, t := range p.Targets {
		if t.OS == "linux" {
			target.Platforms = append(target.Platforms, t)
		}
	}
	if len(target.Platforms) == 0 {
		p.addAt(p.posOf("image"), "image", Fail, "an image was asked for but no linux target is built")
		return
	}

	if base := p.Config.Image.Base; base != "" {
		parsed, err := oci.ParseReference(base)
		if err != nil {
			p.addAt(p.posOf("image"), "image", Fail, "image base: %v", err)
			return
		}
		target.Base = parsed
	}

	p.Image = target
	p.note("image", ref.Registry+"/"+ref.Repository, source)

	if target.Base == (oci.Reference{}) {
		p.add("image", Pass, "%s on scratch, for %d platform(s)", reference, len(target.Platforms))
		return
	}
	p.checkBase(ctx, reference, target)
}

// checkBase resolves the base image while planning rather than while building.
//
// Whether a base can be fetched is a fact about the release, and plan's whole
// purpose is to surface those in two seconds rather than after a cross-compile
// of every target. Resolving it here also turns the "named by tag" warning
// from advice into an instruction: it can name the digest to pin.
//
// One platform is enough to answer the question. The release resolves each
// one and caches them, which is a different job.
func (p *Plan) checkBase(ctx context.Context, reference string, target *ImageTarget) {
	registry := oci.NewRegistry(target.Base.APIHost())
	registry.UserAgent = "letsgo"

	platform := oci.Platform{OS: "linux", Architecture: target.Platforms[0].Arch}

	base, err := oci.ResolveBase(ctx, registry, target.Base, platform)

	status, detail := baseResult(reference, target, base, err)
	p.add("image", status, "%s", detail)
}

// baseResult judges a base resolution.
//
// Separated from the fetch above because the judgement is the part worth
// testing: whether a failure is the config's fault turns on who answered, and
// that distinction should not need a registry to exercise.
func baseResult(reference string, target *ImageTarget, base *oci.Base, err error) (Status, string) {
	where := fmt.Sprintf("%s on %s, for %d platform(s)", reference, target.Base, len(target.Platforms))

	if err != nil {
		// A registry that answered is reporting a real problem with the
		// reference — the wrong repository, a tag that does not exist, a
		// private image. One that could not be reached says nothing about the
		// config, and planning offline is worth keeping.
		var answered *oci.Error
		if errors.As(err, &answered) {
			return Fail, err.Error()
		}
		return Warn, fmt.Sprintf(
			"%s\n  the base could not be resolved, so it was not checked: %v", where, err)
	}

	if target.Base.Digest == "" {
		// A tag is a moving target. The resolved digest is recorded in the
		// manifest either way, so this is a warning rather than a refusal —
		// and naming the digest makes the fix a copy and paste.
		return Warn, fmt.Sprintf(
			"%s\n  the base is named by tag, so two releases of the same commit can differ; "+
				"pin it with @%s", where, base.IndexDigest)
	}
	return Pass, where
}

// checkTap establishes that the formula has somewhere to go before anything is
// built. A release that succeeds and then cannot update the tap has left the
// two out of step, which is worse than not starting.
func (p *Plan) checkTap(ctx context.Context, client *github.Client, source string) {
	if p.Tap == (github.Repo{}) {
		return
	}

	access, err := client.CheckAccess(ctx, p.Tap)
	switch {
	case err != nil:
		p.add(brewTap, Fail, "%v", err)
	case access.Archived:
		p.add(brewTap, Fail, "%s is archived and cannot receive a formula", p.Tap)
	case access.CanPush:
		p.add(brewTap, Pass, "%s can receive the formula, with %s", p.Tap, source)
	case underActions():
		// Same limitation as the release token: an installation token's
		// permissions are not described by the repository endpoint, and a
		// tap in another repository needs a token this one cannot inspect.
		p.add(brewTap, Warn,
			"whether %s can write to %s cannot be confirmed from inside Actions\n"+
				"  a workflow token cannot write to another repository; set %s to an App token scoped to the tap",
			source, p.Tap, TapTokenEnvVars[0])
	default:
		p.add(brewTap, Fail, "%s cannot write to %s", source, p.Tap)
	}
}

func (p *Plan) loadConfig(moduleDir string) {
	path := filepath.Join(moduleDir, ConfigFile)

	data, err := os.ReadFile(path)
	if err != nil {
		// Absence is the primary path, not a problem.
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.note("config", "none", "zero-config defaults")
		return
	}

	file, err := config.Parse(ConfigFile, data)
	if err != nil {
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.add("config", Fail, "%v", err)
		return
	}
	cfg, err := config.Decode(file)
	if err != nil {
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.add("config", Fail, "%v", err)
		return
	}

	p.Config, p.ConfigPath = cfg, path
	p.note("config", ConfigFile, "repository root")
}

// resolveFeatures folds letsgo.mod's `disable` directive together with any
// one-run flag that means the same thing, so the rest of the plan has one
// place to ask whether a feature is on.
func (p *Plan) resolveFeatures(opts Options) {
	disabled := append([]string(nil), p.Config.Disabled...)

	from := ConfigFile
	if opts.DisableProxyWarm {
		if len(disabled) == 0 {
			from = "--no-proxy-warm"
		} else {
			from += ", --no-proxy-warm"
		}
		disabled = append(disabled, "proxy-warm")
	}

	p.Features = feature.Resolve(disabled)
	p.Required = append([]string(nil), p.Config.Required...)
	sort.Strings(p.Required)

	if len(p.Features) == 0 && len(p.Required) == 0 {
		return
	}

	var parts []string
	if len(p.Features) > 0 {
		parts = append(parts, "disabled: "+strings.Join(p.Features.Disabled(), ", "))
	}
	if len(p.Required) > 0 {
		parts = append(parts, "required: "+strings.Join(p.Required, ", "))
	}
	detail := strings.Join(parts, "; ")

	p.note("features", detail, from)
	p.add("features", Pass, "%s", detail)
}

// required reports whether a feature's Skip must be a Fail instead.
func (p *Plan) required(name string) bool {
	for _, n := range p.Required {
		if n == name {
			return true
		}
	}
	return false
}

// skip records a check as Skip, unless the feature behind it is required —
// in which case a Skip is exactly the outcome require promised would not
// happen.
func (p *Plan) skip(check, feature, format string, args ...any) {
	if p.required(feature) {
		p.addAt(p.posOf("require "+feature), check, Fail, format, args...)
		return
	}
	p.add(check, Skip, format, args...)
}

// resolveModule settles which module is built.
//
// Without the directive that is the module beside letsgo.mod, which is what
// every single-module repository has. With it, a repository whose binary lives
// in a second module — a workspace where the renderer is split out so that
// importing the core does not pull in its dependencies — can release that
// module while still being one project with one version and one tag.
func (p *Plan) resolveModule() {
	if p.Config.ModuleDir == "" {
		p.note("module", p.Module.Path, "go.mod")
		return
	}

	dir := filepath.Join(p.RootDir, filepath.FromSlash(p.Config.ModuleDir))
	module, err := discover.FindModule(dir)
	if err != nil {
		p.addAt(p.posOf("module"), "module", Fail, "module %s: %v", p.Config.ModuleDir, err)
		return
	}

	// FindModule walks up, so a directory with no go.mod of its own resolves
	// to an ancestor's. That is a silent no-op rather than the nested module
	// that was asked for, and worth saying.
	if module.Dir != dir {
		p.addAt(p.posOf("module"), "module", Fail, "module %s: no go.mod in that directory", p.Config.ModuleDir)
		return
	}

	p.Module = module
	p.note("module", module.Path, ConfigFile)
	p.add("module", Pass, "%s in %s", module.Path, p.Config.ModuleDir)

	// The tag names the repository's release, not a version of this nested
	// module — v1.2.0 is not a tag `go install module/web@v1.2.0` or the
	// module proxy will ever recognise. Scoped tags (a prefixed tag naming
	// this module directly) are what fixes this; until then, a warm that can
	// only fail is skipped rather than reported as a broken release.
	p.add("module proxy", Warn,
		"the tag has no %s prefix, so `go install %s@version` and the proxy warm cannot resolve it",
		p.Config.ModuleDir, module.Path)
}

// checkReplace fails a release whose go.mod replaces a dependency with a
// local filesystem path, rather than letting the failure surface later as an
// unrelated-looking build or reproducibility error.
func (p *Plan) checkReplace() {
	module, dir, err := discover.LocalReplace(filepath.Join(p.Module.Dir, "go.mod"))
	if err != nil {
		p.add("replace", Fail, "%v", err)
		return
	}
	if module != "" {
		p.add("replace", Fail,
			"go.mod replaces %s with the local path %q\n"+
				"  `go install` cannot resolve a local replacement, and the release's source archive does not contain it either",
			module, dir)
		return
	}
	p.add("replace", Pass, "no local-path replace directives")
}

func (p *Plan) resolveProject() {
	if p.Config.Project != "" {
		p.Project = p.Config.Project
		p.note("project", p.Project, ConfigFile)
		return
	}
	// With a nested module the project is still the repository's: releasing
	// ./web does not make the project "web", and the archives, the formula and
	// the image would all be named after a directory.
	if p.Config.ModuleDir != "" && p.root.Name != "" {
		p.Project = p.root.Name
		p.note("project", p.Project, "last element of the repository's module path")
		return
	}

	p.Project = p.Module.Name
	p.note("project", p.Project, "last element of the module path")
}

func (p *Plan) resolveVersion(ctx context.Context, opts Options) {
	if p.Snapshot {
		base := "0.0.0"
		if prev, err := discover.PreviousTag(ctx, p.GitBin, p.RootDir, p.Scope.Prefix); err == nil && prev != "" {
			base = strings.TrimPrefix(strings.TrimPrefix(prev, p.Scope.Prefix), "v")
		}
		p.Version = fmt.Sprintf("%s-next+%s", base, p.Git.ShortCommit)
		p.note("version", p.Version, "snapshot of the previous tag")
		return
	}

	prefix := p.Scope.Prefix
	versions := scopedVersionTags(p.Git.Tags, prefix)

	// opts.Tag pins which of several qualifying tags on HEAD is being
	// released. Ordinarily HEAD carries at most one, but `letsgo promote`
	// tags an RC's own commit with its stable version, and that commit
	// already carries the RC's own tag — so for that one caller, and only
	// when it says so explicitly, more than one tag on HEAD is not an error.
	if opts.Tag != "" {
		if !slices.Contains(versions, opts.Tag) {
			p.add("tag", Fail, "%s does not carry the requested tag %s", p.Git.Commit, opts.Tag)
			return
		}
		versions = []string{opts.Tag}
	}

	switch len(versions) {
	case 0:
		p.failNoVersionTag(prefix)
		return
	case 1:
		// The ordinary case.
	default:
		p.add("tag", Fail, "HEAD carries several version tags (%s); it is ambiguous which is being released",
			strings.Join(versions, ", "))
		return
	}

	p.Tag = versions[0]
	versionTag := strings.TrimPrefix(p.Tag, prefix)
	p.Version = strings.TrimPrefix(versionTag, "v")
	p.note("version", p.Version, "git tag "+p.Tag)
	p.add("tag", Pass, "%s is the only version tag on HEAD", p.Tag)

	// The check nothing else in this category performs.
	if err := p.Module.CheckTag(versionTag); err != nil {
		p.add("module path", Fail, "%v", err)
	} else {
		p.add("module path", Pass, "%s agrees with tag %s", p.Module.Path, p.Tag)
	}
}

// scopedVersionTags returns the tags that name a version of prefix's own
// scope, in the same order they were given.
func scopedVersionTags(tags []string, prefix string) []string {
	scope := discover.Scope{Prefix: prefix}
	var versions []string
	for _, tag := range tags {
		if _, ok := scope.MatchesTag(tag); ok {
			versions = append(versions, tag)
		}
	}
	return versions
}

// failNoVersionTag records that this scope has no version tag on HEAD,
// naming its own prefix when it has one: a root module's tag is a different
// scope entirely, not this one's missing tag.
func (p *Plan) failNoVersionTag(prefix string) {
	if prefix == "" {
		p.add("tag", Fail, "HEAD has no version tag; tag a release or use --snapshot")
		return
	}
	p.add("tag", Fail, "HEAD has no %sv version tag; tag a release or use --snapshot", prefix)
}

func (p *Plan) checkWorktree(opts Options) {
	switch {
	case p.Git.Clean:
		p.add("worktree", Pass, "clean")
	case opts.AllowDirty || opts.Snapshot:
		p.add("worktree", Warn, "uncommitted changes; artifacts will not be reproducible from this commit")
	default:
		p.add("worktree", Fail, "uncommitted changes; a release must be reproducible from its commit alone")
	}
}

func (p *Plan) resolveTargets(ctx context.Context) {
	if len(p.Config.Targets) > 0 {
		targets, err := gobuild.ParseTargets(p.Config.Targets)
		if err != nil {
			p.addAt(p.posOf("build"), "targets", Fail, "%v", err)
			return
		}
		p.Targets = targets
		p.note("targets", summarise(targets), ConfigFile)
	} else {
		p.Targets = gobuild.ReleaseTargets
		p.note("targets", summarise(p.Targets), "default matrix")
	}

	if err := gobuild.Validate(ctx, "", p.Targets); err != nil {
		p.addAt(p.posOf("build"), "targets", Fail, "%v", err)
		return
	}
	p.add("targets", Pass, "%d targets, all buildable by this toolchain", len(p.Targets))

	p.resolveTags()
}

// resolveTags reads the build tags.
//
// Tags are recorded in the manifest and replayed by verification, so a tagged
// build is exactly as reproducible as an untagged one: the tag list is config,
// and config is pinned by the commit.
func (p *Plan) resolveTags() {
	if len(p.Config.Tags) == 0 {
		return
	}
	for _, tag := range p.Config.Tags {
		if !validTag(tag) {
			p.addAt(p.posOf("tags"), "tags", Fail, "%q is not a build tag", tag)
			return
		}
	}
	p.Tags = p.Config.Tags
	p.note("tags", strings.Join(p.Tags, ", "), ConfigFile)
}

// validTag reports whether a build tag is one the toolchain will accept.
// Checked here because `-tags` takes a comma-separated list, so a tag
// containing a comma would silently become two.
func validTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// resolveBudgets parses the configured size caps.
//
// A budget naming a target that is not built would silently never apply,
// which is the failure mode a size budget exists to prevent, so it is a
// mistake rather than a no-op.
func (p *Plan) resolveBudgets() {
	if len(p.Config.Budgets) == 0 {
		return
	}

	built := make(map[string]bool, len(p.Targets))
	for _, t := range p.Targets {
		built[t.String()] = true
	}

	budgets := make(map[string]bytesize.Size, len(p.Config.Budgets))
	failed := false
	for _, target := range sortedKeys(p.Config.Budgets) {
		pos := p.configPos(p.Config.BudgetPos[target])
		size, err := bytesize.Parse(p.Config.Budgets[target])
		if err != nil {
			p.addAt(pos, "budgets", Fail, "budget %s: %v", target, err)
			failed = true
			continue
		}
		if !built[target] {
			p.addAt(pos, "budgets", Fail,
				"budget names %s, which is not a target this release builds", target)
			failed = true
			continue
		}
		budgets[target] = size
	}

	if failed {
		return
	}
	p.Budgets = budgets

	described := make([]string, 0, len(budgets))
	for _, target := range sortedKeys(budgets) {
		described = append(described, target+" "+budgets[target].String())
	}
	p.note("budgets", strings.Join(described, ", "), ConfigFile)
	p.add("budgets", Pass, "%d target(s) capped; checked once the binaries exist", len(budgets))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// versionVars are the conventional linker-injected variables.
var versionVars = []string{"version", "commit", "date"}

func (p *Plan) resolveCommands(ctx context.Context) {
	commands, err := discover.FindMainPackages(p.Module.Dir, p.Project)
	if err != nil {
		p.add("commands", Fail, "%v", err)
		return
	}
	p.Commands = commands

	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.RelPath
	}
	p.note("commands", strings.Join(names, ", "), "./cmd/* or the module root")
	p.add("commands", Pass, "%d main package(s)", len(commands))

	p.resolveLDFlags(ctx)
}

// resolveLDFlags injects version metadata only into variables that can
// actually receive it.
//
// The linker accepts -X for a symbol that does not exist without complaint, so
// a wrong name produces a binary reporting its compiled-in default forever.
// Checking first turns that silent failure into a message at plan time, and
// injecting only what works means a project that does not declare these
// variables is not handed flags that do nothing.
// VersionSymbols names the variables the version metadata is injected into,
// fully qualified as the linker writes them.
type VersionSymbols struct {
	Version string
	Commit  string
	Date    string
}

func (p *Plan) resolveLDFlags(ctx context.Context) {
	p.LDFlags = append(p.LDFlags, p.Config.LDFlags...)
	p.applyLDFlagsPlugin(ctx)
	p.Symbols = VersionSymbols{Version: "main.version", Commit: "main.commit", Date: "main.date"}

	if p.Config.Version != nil {
		p.resolveVersionSymbols()
		return
	}
	p.checkMainVersionVars()
}

// versionInjection is the check that reports which variables the release will
// write its version into, whether the config named them or letsgo inferred
// them from main.
const versionInjection = "version injection"

// resolveVersionSymbols checks the variables the config named.
//
// A configured symbol is checked harder than an inferred one: asking for
// injection into a variable that does not exist is a mistake, where simply not
// declaring main.version is a choice. Getting this wrong is exactly the silent
// failure that ships binaries reporting their compiled-in default.
func (p *Plan) resolveVersionSymbols() {
	targets := []struct {
		label  string
		symbol string
		into   *string
	}{
		{"version", p.Config.Version.Version, &p.Symbols.Version},
		{"commit", p.Config.Version.Commit, &p.Symbols.Commit},
		{"date", p.Config.Version.Date, &p.Symbols.Date},
	}

	var problems []string
	checked := 0

	for _, t := range targets {
		if t.symbol == "" {
			continue
		}
		qualified, dir, err := p.locateSymbol(t.symbol)
		if err != nil {
			problems = append(problems, fmt.Sprintf("version %s: %v", t.label, err))
			continue
		}

		_, name, _ := cutSymbol(qualified)
		symbols, err := discover.InspectVars(dir, []string{name})
		if err != nil {
			problems = append(problems, fmt.Sprintf("version %s: %v", t.label, err))
			continue
		}

		sym := symbols[0]
		if sym.Status != discover.SymbolOK {
			detail := fmt.Sprintf("version %s: %s: %s", t.label, qualified, sym.Detail)
			if sym.Status == discover.SymbolMissing {
				detail = fmt.Sprintf("version %s: %s is not declared", t.label, qualified)
			}
			if sym.Suggestion != "" {
				detail += " (" + sym.Suggestion + ")"
			}
			problems = append(problems, detail)
			continue
		}

		*t.into = qualified
		checked++
	}

	if len(problems) > 0 {
		p.addAt(p.posOf("version"), versionInjection, Fail, "%s", strings.Join(problems, "\n"))
		return
	}
	p.add(versionInjection, Pass, "%d symbol(s) verified before injection", checked)
	p.note("version symbols", strings.Join(p.injectedSymbols(), ", "), ConfigFile)
}

func (p *Plan) injectedSymbols() []string {
	return []string{p.Symbols.Version, p.Symbols.Commit, p.Symbols.Date}
}

// locateSymbol turns a configured symbol into the form the linker needs and
// the directory holding its package.
//
// A package path may be written whole or relative to the module, because a
// module path is long and repeating it in every entry is noise. Either way the
// package has to exist in this module: injecting into a variable letsgo cannot
// see would be the unchecked -X that the gate exists to prevent.
func (p *Plan) locateSymbol(symbol string) (qualified, dir string, err error) {
	pkg, name, ok := cutSymbol(symbol)
	if !ok || name == "" {
		return "", "", fmt.Errorf("%s must name a package and a variable, as in internal/buildinfo.Version", symbol)
	}

	rel := ""
	switch {
	case pkg == p.Module.Path:
	case strings.HasPrefix(pkg, p.Module.Path+"/"):
		rel = strings.TrimPrefix(pkg, p.Module.Path+"/")
	default:
		rel, pkg = pkg, p.Module.Path+"/"+pkg
	}

	dir = filepath.Join(p.Module.Dir, filepath.FromSlash(rel))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("%s is not a package in %s", pkg, p.Module.Path)
	}
	return pkg + "." + name, dir, nil
}

// cutSymbol splits a linker symbol at its final dot, which is where the linker
// splits it: a package path contains dots of its own.
func cutSymbol(symbol string) (pkg, name string, ok bool) {
	i := strings.LastIndex(symbol, ".")
	if i <= 0 {
		return "", "", false
	}
	return symbol[:i], symbol[i+1:], true
}

// checkMainVersionVars is the inferred path: main.version, main.commit and
// main.date in each command, injected where they are declared.
func (p *Plan) checkMainVersionVars() {
	var problems []string
	injected := 0

	for _, cmd := range p.Commands {
		names := make([]string, len(versionVars))
		copy(names, versionVars)

		symbols, err := discover.InspectVars(cmd.Dir, names)
		if err != nil {
			p.add(versionInjection, Warn, "%v", err)
			return
		}

		for _, sym := range symbols {
			switch sym.Status {
			case discover.SymbolOK:
				injected++
			case discover.SymbolMissing:
				// Not declaring these is a legitimate choice, so their absence
				// is silent. Declaring one wrongly is not.
			default:
				detail := fmt.Sprintf("main.%s in %s: %s", sym.Name, cmd.RelPath, sym.Detail)
				if sym.Suggestion != "" {
					detail += " (" + sym.Suggestion + ")"
				}
				problems = append(problems, detail)
			}
		}
	}

	switch {
	case len(problems) > 0:
		p.add(versionInjection, Fail, "%s", strings.Join(problems, "\n"))
	case injected == 0:
		p.add(versionInjection, Skip,
			"no main.version, main.commit or main.date declared; name one with `version <symbol>`")
	default:
		p.add(versionInjection, Pass, "%d symbol(s) verified before injection", injected)
	}
}

// archiveFiles is the check that reports what the archives will carry beside
// the binaries, however that list was arrived at.
const archiveFiles = "archive files"

func (p *Plan) resolveFiles(ctx context.Context) {
	if len(p.Config.ArchiveFiles) > 0 {
		files, err := p.expandArchiveFiles(ctx, p.Config.ArchiveFiles)
		if err != nil {
			p.addAt(p.posOf("archive"), archiveFiles, Fail, "%v", err)
			return
		}
		p.Files = files
		p.note(archiveFiles, strings.Join(p.Files, ", "), ConfigFile)
		return
	}

	// Conventional documentation, included when present. The list lives in
	// internal/build so verification reaches the same answer from the same
	// tree rather than keeping a second copy of it.
	p.Files = build.FindDocumentation(p.RootDir)

	if len(p.Files) > 0 {
		p.note(archiveFiles, strings.Join(p.Files, ", "), "found in the repository root")
	}
}

// expandArchiveFiles turns the configured entries into the exact file list the
// archives will contain.
//
// A directory expands to the tracked files beneath it, sorted. That is pinned
// by the commit as tightly as a literal list is — the tree at a commit is
// fixed — while staying correct as the directory changes, which a hand-written
// list does not: adding a file to it and forgetting the config ships a release
// missing the file, and nothing fails, because nothing was asked for.
//
// A glob is refused rather than expanded. It would resolve against whatever is
// on disk at release time, which is an input the config does not pin.
func (p *Plan) expandArchiveFiles(ctx context.Context, entries []string) ([]string, error) {
	var tracked []string

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.ContainsAny(entry, "*?[") {
			return nil, fmt.Errorf(
				"archive %s: archive takes paths, not patterns; name a file or a directory", entry)
		}

		info, err := os.Stat(filepath.Join(p.RootDir, filepath.FromSlash(entry)))
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", entry, err)
		}
		if !info.IsDir() {
			out = append(out, entry)
			continue
		}

		// Read once, and only when a directory is actually named.
		if tracked == nil {
			if tracked, err = discover.TrackedFiles(ctx, p.GitBin, p.RootDir); err != nil {
				return nil, fmt.Errorf("archive %s: %w", entry, err)
			}
		}

		under := filesUnder(tracked, entry)
		if len(under) == 0 {
			return nil, fmt.Errorf("archive %s: the directory holds no tracked files", entry)
		}
		out = append(out, under...)
	}

	sort.Strings(out)
	return out, nil
}

// filesUnder returns the tracked files inside dir, which git already reports
// in sorted, slash-separated form.
func filesUnder(tracked []string, dir string) []string {
	prefix := path.Clean(filepath.ToSlash(dir)) + "/"

	var found []string
	for _, name := range tracked {
		if strings.HasPrefix(name, prefix) {
			found = append(found, name)
		}
	}
	return found
}

// checkNotesRequired makes a required notes section's absence a Fail. Both
// sections follow the changelog, so `disable changelog` leaves them unwritten;
// a release that required one asked for exactly that not to happen.
func (p *Plan) checkNotesRequired() {
	if p.Features.On("changelog") {
		return
	}
	for _, name := range []string{"diff-notes", "randomart"} {
		if p.required(name) && p.Features.On(name) {
			p.addAt(p.posOf("require "+name), name, Fail, "required, but changelog is disabled, so no release notes are written")
		}
	}
}

// installScript is the check name checkInstallScriptRequired reports under.
const (
	installScript = "install script"

	// The names a check and its directive go by in the config.
	pluginDirective      = "plugin "
	requireInstallScript = "require install-script"
	brewTap              = "brew tap"
)

// checkInstallScriptRequired makes install.sh's absence a Fail rather than
// silent, for a repository that required it.
//
// Every other case (no tag, no target platform install.sh supports) is
// already how writeInstaller decides not to write one; require only asks
// that letsgo say so at plan time instead of after a release that shipped
// without it.
func (p *Plan) checkInstallScriptRequired() {
	if !p.required("install-script") {
		return
	}

	switch {
	case p.Tag == "":
		p.addAt(p.posOf(requireInstallScript), installScript, Fail, "required, but there is no tag to build one for")
	case !p.HasRepo || p.Repo.Host != "github.com":
		p.addAt(p.posOf(requireInstallScript), installScript, Fail, "required, but the repository is not on GitHub")
	case len(installablePlatforms(p.Targets)) == 0:
		p.addAt(p.posOf(requireInstallScript), installScript, Fail, "required, but no built target is one install.sh supports")
	default:
		p.add(installScript, Pass, "will be generated")
	}
}

// installablePlatforms is which of the release's targets install.sh would
// carry, reusing the installer's own filter so the two can never disagree.
func installablePlatforms(targets []gobuild.Target) []string {
	ts := make([]install.Target, len(targets))
	for i, t := range targets {
		ts[i] = install.Target{OS: t.OS, Arch: t.Arch}
	}
	return install.Platforms(ts)
}

func (p *Plan) resolveArtifacts(ctx context.Context) {
	if p.Version == "" || len(p.Commands) == 0 {
		return
	}

	p.Groups = p.defaultGroups()
	p.applyLayoutPlugin(ctx)
	p.addVariantGroups(ctx)

	for _, group := range p.Groups {
		for _, target := range group.Targets {
			format := archive.FormatTarGz
			if target.OS == "windows" {
				format = archive.FormatZip
			}
			p.Artifacts = append(p.Artifacts, Artifact{
				Name: fmt.Sprintf("%s_%s_%s_%s%s",
					group.Name, p.Version, target.OS, target.Arch, format.Ext()),
				Target:   target,
				Commands: group.Commands,
				Format:   format,
			})
		}
	}
}

// defaultGroups is one archive per command, which is what letsgo has always
// produced and what a single-command repository wants.
func (p *Plan) defaultGroups() []Group {
	groups := make([]Group, 0, len(p.Commands))
	for _, cmd := range p.Commands {
		name := p.Project
		// With several commands the project name cannot identify an artifact.
		if len(p.Commands) > 1 {
			name = cmd.BinaryName
		}
		groups = append(groups, Group{
			Name:     name,
			Commands: []discover.MainPackage{cmd},
			Targets:  p.Targets,
			Tags:     p.Tags,
		})
	}
	return groups
}

// addVariantGroups appends a second build of the same commands for each
// variant.
//
// The commands and the layout are the release's; only how they compile and
// where they run differ. A repository with a headless CLI and a GUI build
// behind a tag ships both from one commit, and the suffix is what keeps their
// archives apart.
func (p *Plan) addVariantGroups(ctx context.Context) {
	if len(p.Config.Variants) == 0 {
		return
	}

	base := len(p.Groups)
	names := make([]string, 0, len(p.Config.Variants))

	for _, variant := range p.Config.Variants {
		targets, err := gobuild.ParseTargets(variant.Targets)
		if err != nil {
			p.variantFailed(variant.Name, err)
			return
		}
		if err := gobuild.Validate(ctx, "", targets); err != nil {
			p.variantFailed(variant.Name, err)
			return
		}

		for _, group := range p.Groups[:base] {
			p.Groups = append(p.Groups, Group{
				Name:     group.Name + "-" + variant.Name,
				Commands: group.Commands,
				Targets:  targets,
				// The release's tags first: a variant adds to how the module
				// builds rather than replacing it.
				Tags:    append(append([]string{}, p.Tags...), variant.Tags...),
				Variant: variant.Name,
			})
		}
		names = append(names, variant.Name)
	}

	p.note("variants", strings.Join(names, ", "), ConfigFile)
	p.add("variants", Pass, "%d variant(s): %s", len(names), strings.Join(names, ", "))
}

// variantFailed reports a problem with one variant, naming which. A repository
// can declare several, and a message that did not say which one would leave
// the reader to guess.
func (p *Plan) variantFailed(name string, err error) {
	p.add("variants", Fail, "variant %s: %v", name, err)
}

func summarise(targets []gobuild.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.String()
	}
	return strings.Join(names, ", ")
}

// sumdbCheck is the check name checkSumdb reports under, and sumdbGate the
// feature behind it.
const (
	sumdbCheck = "sumdb"
	sumdbGate  = "sumdb"
)

// checkSumdb records why the sum.golang.org cross-check will not run, so a
// required sumdb turns that into a Fail. Nothing is added when it will run:
// the check itself happens during release, after the module proxy has been
// primed and before any asset is attached.
func (p *Plan) checkSumdb() {
	switch {
	case !p.Features.On(sumdbGate):
		p.add(sumdbCheck, Skip, disabledByConfig)
	case p.Snapshot || p.Tag == "":
		p.skip(sumdbCheck, sumdbGate, "not a tagged release")
	case p.Config.ModuleDir != "":
		p.skip(sumdbCheck, sumdbGate, "the module is not at the repository root")
	case !p.Features.On("proxy-warm"):
		p.skip(sumdbCheck, sumdbGate, "proxy-warm is disabled, so sum.golang.org has no record to compare")
	default:
		if skip, reason := sumdb.PrivateModule(p.Module.Path); skip {
			p.skip(sumdbCheck, sumdbGate, "private module (%s)", reason)
		}
	}
}
