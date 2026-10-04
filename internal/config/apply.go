package config

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/danielriddell21/letsgo/modsyntax"

	"github.com/danielriddell21/letsgo/internal/feature"
)

// apply folds one directive into the config. Each directive gets its own
// function: the shapes have nothing in common beyond the keyword, and a single
// switch grew into something nobody could read at a glance.
func apply(cfg *Config, file string, line *modsyntax.Line) error {
	if handle, ok := handlers[line.Keyword]; ok {
		return handle(cfg, file, line)
	}
	if blockOnly[line.Keyword] {
		return errAt(file, line.P, "%s", modDirectives.usage[line.Keyword])
	}
	return nil
}

func applyProject(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) != 1 {
		return arity(file, line)
	}
	cfg.Project = line.Args[0]
	return nil
}

func applyBuild(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.Targets = append(cfg.Targets, line.Args...)
	return nil
}

func applyTags(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.Tags = append(cfg.Tags, line.Args...)
	return nil
}

// variantDirectives are what a variant may set.
//
// Deliberately two: a variant is the same source built differently, so it can
// change how it compiles and where it runs, and nothing else. Letting it set a
// project name or a tap would make it a second release configuration wearing
// the same file.
var variantDirectives = map[string]bool{"build": true, "tags": true}

// applyVariant reads one variant block.
//
// The inner lines are applied to a throwaway config and the results lifted
// out, so a variant's `build` and `tags` are parsed and validated by exactly
// the code that parses the release's own.
func applyVariant(cfg *Config, file string, b *modsyntax.Block) error {
	if len(b.Args) != 1 {
		return errAt(file, b.P, "%s", modDirectives.usage["variant"])
	}
	name := b.Args[0]

	if name == "" || strings.ContainsAny(name, "_/ ") {
		return errAt(file, b.P,
			"variant %q: the name suffixes an archive, so it cannot contain a space, slash or underscore", name)
	}
	for _, existing := range cfg.Variants {
		if existing.Name == name {
			return errAt(file, b.P, "variant %s is already defined", name)
		}
	}

	// A block's lines arrive as arguments to the block's own keyword, so the
	// directive each one means is its first word — the same shape as
	// `image ( base ... )`.
	inner := &Config{Budgets: map[string]string{}, BudgetPos: map[string]modsyntax.Position{}}
	for _, line := range b.Lines {
		if len(line.Args) == 0 {
			continue
		}
		keyword, args := line.Args[0], line.Args[1:]
		if !variantDirectives[keyword] {
			return errAt(file, line.P,
				"a variant sets build and tags; %q belongs outside it", keyword)
		}
		if err := apply(inner, file, &modsyntax.Line{Keyword: keyword, Args: args, P: line.P}); err != nil {
			return err
		}
	}

	// Without its own targets a variant would build the whole matrix, which is
	// never what one is for: the GUI half of a repository exists precisely
	// because it does not run everywhere the CLI does.
	if len(inner.Targets) == 0 {
		return errAt(file, b.P, "variant %s must name the targets it builds for", name)
	}

	cfg.Variants = append(cfg.Variants, Variant{Name: name, Targets: inner.Targets, Tags: inner.Tags})
	return nil
}

// applyPlugin reads one plugin pin.
//
// Four fields and no defaults: the hook says when it runs, the command what to
// run, the version what was asked for, and the digest what actually has to be
// on disk. Nothing here is optional, because a plugin that ran unpinned would
// be an unrecorded build input — the thing the whole contract exists to
// prevent.
func applyPlugin(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) != 4 {
		return arity(file, line)
	}

	hook, command, version, digest := line.Args[0], line.Args[1], line.Args[2], line.Args[3]

	for _, existing := range cfg.Plugins {
		if existing.Hook == hook {
			return errAt(file, line.P, "a plugin for the %s hook is already set", hook)
		}
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return errAt(file, line.P, "plugin %s: %q is not a sha256 digest", command, digest)
	}

	cfg.mark("plugin "+hook, line.P)
	cfg.Plugins = append(cfg.Plugins, Plugin{
		Hook: hook, Command: command, Version: version, Digest: digest,
	})
	return nil
}

func applyLDFlags(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.LDFlags = append(cfg.LDFlags, line.Args...)
	return nil
}

func applyArchive(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.ArchiveFiles = append(cfg.ArchiveFiles, line.Args...)
	return nil
}

// applyModule reads the module directory.
//
// It must stay inside the repository: the path ends up joined to the root and
// then handed to the toolchain, so an absolute path or one climbing out with
// ".." would silently build something the commit does not contain.
func applyModule(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) != 1 {
		return arity(file, line)
	}

	dir := path.Clean(filepath.ToSlash(line.Args[0]))

	switch {
	case dir == "." || dir == "":
		// Naming the root is the default, so saying it is harmless.
		return nil
	case path.IsAbs(dir), filepath.IsAbs(line.Args[0]):
		return errAt(file, line.P, "module %s must be relative to the repository root", line.Args[0])
	case dir == "..", strings.HasPrefix(dir, "../"):
		return errAt(file, line.P, "module %s leaves the repository", line.Args[0])
	}

	cfg.ModuleDir = dir
	return nil
}

// applyVersion reads where the version metadata is injected.
//
// The bare form names the version's variable, which is the case worth being
// short; commit and date are keyed, and follow the same shape as the image
// directive's settings.
func applyVersion(cfg *Config, file string, line *modsyntax.Line) error {
	if cfg.Version == nil {
		cfg.Version = &VersionSymbols{}
	}

	var (
		field  *string
		label  string
		symbol string
	)
	switch {
	case len(line.Args) == 1:
		field, label, symbol = &cfg.Version.Version, "version", line.Args[0]
	case len(line.Args) == 2 && line.Args[0] == "commit":
		field, label, symbol = &cfg.Version.Commit, "version commit", line.Args[1]
	case len(line.Args) == 2 && line.Args[0] == "date":
		field, label, symbol = &cfg.Version.Date, "version date", line.Args[1]
	default:
		return arity(file, line)
	}

	if _, name, ok := cutSymbol(symbol); !ok || name == "" {
		return errAt(file, line.P,
			"%s %s must name a package and a variable, as in internal/buildinfo.Version", label, symbol)
	}
	if *field != "" {
		return errAt(file, line.P, "%s is already set", label)
	}
	*field = symbol
	return nil
}

// cutSymbol splits a linker symbol into its package path and variable name at
// the final dot, which is where the linker splits it: a package path may
// contain dots of its own, as every domain-named import path does.
func cutSymbol(symbol string) (pkg, name string, ok bool) {
	i := strings.LastIndex(symbol, ".")
	if i <= 0 {
		return "", "", false
	}
	return symbol[:i], symbol[i+1:], true
}

func applyBudget(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) != 2 {
		return arity(file, line)
	}
	if _, exists := cfg.Budgets[line.Args[0]]; exists {
		return errAt(file, line.P, "budget for %s is already set", line.Args[0])
	}
	cfg.Budgets[line.Args[0]] = line.Args[1]
	cfg.BudgetPos[line.Args[0]] = line.P
	return nil
}

// imageSettings are the keyed forms of the image directive.
//
// One function each, for the same reason apply itself dispatches rather than
// switching: image carries more shapes than any other directive — a bare form,
// a bare reference, and a setting per key — and a single switch over all of
// them is past the point where anyone can read it at a glance.
var imageSettings = map[string]func(*Image, string, *modsyntax.Line) error{
	"base":   applyImageBase,
	"cmd":    applyImageCmd,
	"expose": applyImageExpose,
}

func applyImage(cfg *Config, file string, line *modsyntax.Line) error {
	if cfg.Image == nil {
		cfg.Image = &Image{}
	}

	// A bare `image` asks for the default: a reference derived from the
	// repository, on scratch.
	if len(line.Args) == 0 {
		return nil
	}
	if setting, ok := imageSettings[line.Args[0]]; ok {
		return setting(cfg.Image, file, line)
	}

	if len(line.Args) != 1 {
		return arity(file, line)
	}
	if cfg.Image.Reference != "" {
		return errAt(file, line.P, "the image reference is already set")
	}
	cfg.Image.Reference = line.Args[0]
	return nil
}

func applyImageBase(img *Image, file string, line *modsyntax.Line) error {
	if len(line.Args) != 2 {
		return arity(file, line)
	}
	if img.Base != "" {
		return errAt(file, line.P, "image base is already set")
	}
	img.Base = line.Args[1]
	return nil
}

func applyImageCmd(img *Image, file string, line *modsyntax.Line) error {
	if len(line.Args) < 2 {
		return arity(file, line)
	}
	if img.Cmd != nil {
		return errAt(file, line.P, "image cmd is already set")
	}
	img.Cmd = line.Args[1:]
	return nil
}

func applyImageExpose(img *Image, file string, line *modsyntax.Line) error {
	if len(line.Args) < 2 {
		return arity(file, line)
	}
	for _, arg := range line.Args[1:] {
		port, err := exposedPort(arg)
		if err != nil {
			return errAt(file, line.P, "%v", err)
		}
		img.Expose = append(img.Expose, port)
	}
	return nil
}

// exposedPort normalises "8080" or "8080/udp" into the image spec's
// "port/proto" form. Validated here rather than passed through, because a
// malformed entry is silent otherwise: nothing reads ExposedPorts except a
// registry UI, so a typo would surface as a missing row months later.
func exposedPort(s string) (string, error) {
	number, proto, ok := strings.Cut(s, "/")
	if !ok {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" {
		return "", fmt.Errorf("image expose %q: protocol must be tcp or udp", s)
	}

	n, err := strconv.Atoi(number)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("image expose %q: %q is not a port between 1 and 65535", s, number)
	}
	return number + "/" + proto, nil
}

// applyBrew reads where the formula goes, and what it says afterwards.
//
// The bare form names the tap, which is the case worth being short; caveats
// are keyed, following the same shape as the version and image directives.
// Repeating either is rejected here rather than by the scalar check, because
// the two forms are separate settings sharing one keyword.
func applyBrew(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 2 && line.Args[0] == "caveats" {
		if cfg.BrewCaveats != "" {
			return errAt(file, line.P, "brew caveats is already set")
		}
		cfg.BrewCaveats = line.Args[1]
		return nil
	}

	if len(line.Args) != 1 {
		return arity(file, line)
	}
	if _, _, ok := strings.Cut(line.Args[0], "/"); !ok {
		return errAt(file, line.P, "brew tap %q must be in owner/repo form", line.Args[0])
	}
	if cfg.BrewTap != "" {
		return errAt(file, line.P, "brew is already set")
	}
	cfg.BrewTap = line.Args[0]
	return nil
}

func applyRelease(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}

	for _, arg := range line.Args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return errAt(file, line.P, "release option %q must be key=value", arg)
		}

		switch key {
		case "prerelease":
			switch value {
			case "auto", "true", "false":
				cfg.Prerelease = value
			default:
				return errAt(file, line.P,
					"release prerelease must be auto, true or false, not %q", value)
			}

		case "draft":
			switch value {
			case "true":
				cfg.Draft = true
			case "false":
				cfg.Draft = false
			default:
				return errAt(file, line.P, "release draft must be true or false, not %q", value)
			}

		case "latest":
			switch value {
			case "auto", "true", "false":
				cfg.Latest = value
			default:
				return errAt(file, line.P,
					"release latest must be auto, true or false, not %q", value)
			}

		default:
			return errAt(file, line.P,
				"unknown release option %q; valid options are draft, latest, prerelease", key)
		}
	}
	return nil
}

// applyDisable turns off features that are on by default.
//
// Each name is checked against the catalogue rather than merely collected:
// an integrity feature refusing outright and an off-by-default one pointing
// at its own directive are both mistakes worth catching here, at the line
// that made them, rather than as a release that silently kept the feature
// on.
func applyDisable(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}

	for _, name := range line.Args {
		f, ok := feature.Lookup(name)
		if !ok {
			return unknownFeature(file, line.P, name)
		}
		if f.Kind == feature.Integrity {
			return errAt(file, line.P, "%s cannot be disabled: it is what letsgo is", name)
		}
		if !f.Disable {
			return errAt(file, line.P, "%s cannot be disabled; remove the `%s` directive instead", name, f.Enable)
		}
		if containsString(cfg.Required, name) {
			return errAt(file, line.P, "%s cannot be both disabled and required", name)
		}
		cfg.mark("disable "+name, line.P)
		if !containsString(cfg.Disabled, name) {
			cfg.Disabled = append(cfg.Disabled, name)
		}
	}
	return nil
}

// applyRequire makes a feature's Skip a Fail: a gate that only ran when its
// tool happened to be on PATH becomes one this release cannot pass without
// it.
func applyRequire(cfg *Config, file string, line *modsyntax.Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}

	for _, name := range line.Args {
		f, ok := feature.Lookup(name)
		if !ok {
			return unknownFeature(file, line.P, name)
		}
		if !f.Require {
			return errAt(file, line.P, "%s cannot be required", name)
		}
		if containsString(cfg.Disabled, name) {
			return errAt(file, line.P, "%s cannot be both disabled and required", name)
		}
		cfg.mark("require "+name, line.P)
		if !containsString(cfg.Required, name) {
			cfg.Required = append(cfg.Required, name)
		}
	}
	return nil
}

// unknownFeature reports a name that is not in the catalogue, with the same
// did-you-mean treatment an unknown directive gets.
func unknownFeature(file string, pos modsyntax.Position, name string) error {
	names := make([]string, len(feature.All))
	for i, f := range feature.All {
		names[i] = string(f.Name)
	}
	sort.Strings(names)
	return unknownName(file, pos, "feature", name, names)
}
