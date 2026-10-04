package plan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

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

	p.note("plugins", strings.Join(named, ", "), config.FileName)
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
