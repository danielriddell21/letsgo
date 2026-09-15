package config

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Config is the semantic content of a letsgo.mod file.
//
// Every field is optional. A repository with no config file at all is the
// primary path, and each field here exists only to override something that
// could not be inferred (see internal/discover).
type Config struct {
	// Project overrides the project name derived from the module path.
	Project string

	// ModuleDir is the directory, relative to the repository root, holding the
	// go.mod to build. Empty means the repository root itself.
	//
	// Only the build moves: the repository is still what the source archive
	// covers and what archive files are resolved against, because a nested
	// module is a detail of the layout rather than a different project.
	ModuleDir string

	// Version names the variables the version metadata is injected into.
	// Nil means the inferred main.version, main.commit and main.date.
	Version *VersionSymbols

	// Plugins are the external programs this repository puts in the middle of
	// its release, keyed by hook. Each is pinned by digest: a program that
	// decides what gets built is a build input.
	Plugins []Plugin

	// Variants are additional builds of the same commands, differing by tags,
	// cgo and target. A repository with a CGO-free CLI and a GUI build behind
	// a tag has two products from one source, and they ship side by side.
	Variants []Variant

	// CGo enables cgo and names the C toolchain to compile it with. Nil means
	// off, which is the default and what every pure-Go repository wants.
	CGo *CGo

	// Tags are build tags. They change the compiled bytes deterministically
	// and are pinned by the commit like any other config, so they cost the
	// reproducibility claim nothing.
	Tags []string

	// Targets overrides the default build matrix, as "goos/goarch" strings.
	// Parsing into gobuild.Target happens at plan time so that an invalid
	// target is reported alongside every other planning error rather than
	// aborting on the first one.
	Targets []string

	// LDFlags are appended to the defaults.
	LDFlags []string

	// ArchiveFiles are extra files to include in each archive.
	ArchiveFiles []string

	// Budgets caps binary size per target, e.g. "linux/amd64" -> "15MB".
	Budgets map[string]string

	// Image describes the container image to publish. Nil means none: a
	// release that creates a package in a registry should be something the
	// repository asked for.
	Image *Image

	// BrewTap is an "owner/repo" Homebrew tap to publish a formula to.
	BrewTap string

	// Prerelease is "auto", "true" or "false".
	Prerelease string

	// Draft creates the release without publishing it.
	Draft bool
}

// Variant is one additional build of the module's commands.
//
// A variant names its own targets rather than inheriting the release's,
// because the reason to have one is usually that it does not build everywhere:
// a GUI build needs cgo and a windowing system, and the point is to ship it
// only where it works.
type Variant struct {
	// Name suffixes the archive, as in "gambit-gui_1.0.0_darwin_arm64".
	Name string

	Targets []string
	Tags    []string
	CGo     *CGo
}

// CGo is the cgo build settings.
//
// The compiler is pinned rather than inherited: two C compilers produce
// different bytes from the same source, so a release built with whatever the
// machine had could not be reproduced anywhere else.
type CGo struct {
	// ZigVersion is the zig release to compile with. Empty means letsgo's
	// default.
	ZigVersion string

	// ZigDigest pins a version letsgo ships no digest for.
	ZigDigest string
}

// Plugin is one external program invoked at a named hook.
type Plugin struct {
	Hook    string
	Command string
	Version string
	Digest  string
}

// VersionSymbols names the variables that receive the version metadata.
//
// Each is a package path and a variable, as the linker writes it —
// "internal/buildinfo.Version" relative to the module, or the fully qualified
// "github.com/you/tool/internal/buildinfo.Version". An empty field keeps the
// inferred main.<name>.
type VersionSymbols struct {
	Version string
	Commit  string
	Date    string
}

// Image is the container image a release publishes.
type Image struct {
	// Reference overrides the default, which is derived from the repository.
	Reference string

	// Base is the image to stack on. Empty means scratch, which is the right
	// answer for a static binary that makes no TLS calls and the wrong one for
	// anything that does.
	Base string

	// Cmd is the default argument list. The entrypoint is inferred from the
	// binary, which for a multi-command binary says nothing about which
	// subcommand should run when the image is started bare.
	Cmd []string

	// Expose are the ports to record, as "port" or "port/proto". Metadata
	// only: nothing is opened, and `docker run -p` works either way.
	Expose []string
}

// known lists every directive, with its arity described for error messages.
// A closed set is the point: an unrecognised directive is a mistake, and
// saying so immediately is better than ignoring it and producing a release
// that quietly does not match what the file asked for.
//
// Kept separate from handlers rather than as one table of {usage, apply}
// pairs, because arity() reads this and every handler calls arity(), which is
// an initialisation cycle Go will not accept. TestEveryDirectiveIsHandled
// holds the two in step.
var known = map[string]string{
	"project": "project <name>",
	"module":  "module <dir>",
	"build":   "build <goos/goarch>... or a build ( ... ) block",
	"tags":    "tags <tag>...",
	"cgo":     "cgo on, or cgo zig <version> [sha256:<digest>]",
	"variant": "a variant <name> ( ... ) block setting build, tags and cgo",
	"plugin":  "plugin <hook> <command> <version> sha256:<digest>",
	"ldflags": "ldflags <flag>...",
	"version": "version <symbol>, or version commit|date <symbol>",
	"archive": "archive <file>... or an archive ( ... ) block",
	"budget":  "budget <goos/goarch> <size>",
	"image":   "image, image <reference>, image base <ref>, image cmd <arg>..., or image expose <port>...",
	"brew":    "brew <owner/tap-repo>",
	"release": "release <key=value>...",
}

// blockOnly names the directives that exist only as a block. Written down so
// that using one as a plain line says so, rather than parsing and quietly
// doing nothing.
var blockOnly = map[string]bool{"variant": true}

// handlers folds each directive into the config.
var handlers = map[string]func(cfg *Config, file string, line *Line) error{
	"project": applyProject,
	"module":  applyModule,
	"build":   applyBuild,
	"tags":    applyTags,
	"cgo":     applyCGo,
	"plugin":  applyPlugin,
	"ldflags": applyLDFlags,
	"version": applyVersion,
	"archive": applyArchive,
	"budget":  applyBudget,
	"image":   applyImage,
	"brew":    applyBrew,
	"release": applyRelease,
}

// Decode interprets a parsed file.
func Decode(f *File) (*Config, error) {
	cfg := &Config{Budgets: map[string]string{}}
	seen := map[string]Position{}

	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *Comment:
			continue
		case *Block:
			if err := decodeBlock(cfg, f.Name, seen, s); err != nil {
				return nil, err
			}
		case *Line:
			if err := decodeLine(cfg, f.Name, seen, s); err != nil {
				return nil, err
			}
		}
	}

	return cfg, nil
}

func decodeBlock(cfg *Config, file string, seen map[string]Position, b *Block) error {
	if err := checkKnown(file, b.Keyword, b.P); err != nil {
		return err
	}

	// A variant is the one block a file may have several of, because having
	// two products from one source is the whole point of it.
	if b.Keyword == "variant" {
		return applyVariant(cfg, file, b)
	}
	if len(b.Args) > 0 {
		return errAt(file, b.P, "%s takes no name before its block", b.Keyword)
	}
	if err := checkOnce(file, seen, b.Keyword, b.P); err != nil {
		return err
	}
	for _, line := range b.Lines {
		if err := apply(cfg, file, line); err != nil {
			return err
		}
	}
	return nil
}

func decodeLine(cfg *Config, file string, seen map[string]Position, line *Line) error {
	if err := checkKnown(file, line.Keyword, line.P); err != nil {
		return err
	}
	// Repeating a scalar directive is ambiguous: one of the two values would
	// silently win. Repeating a list directive is not.
	if isScalar(line.Keyword) {
		if err := checkOnce(file, seen, line.Keyword, line.P); err != nil {
			return err
		}
	}
	return apply(cfg, file, line)
}

func isScalar(keyword string) bool {
	switch keyword {
	case "project", "module", "brew":
		return true
	}
	return false
}

func checkKnown(file, keyword string, pos Position) error {
	if _, ok := known[keyword]; ok {
		return nil
	}

	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)

	msg := fmt.Sprintf("unknown directive %q; valid directives are %s",
		keyword, strings.Join(names, ", "))
	if near := nearestKeyword(keyword, names); near != "" {
		msg = fmt.Sprintf("unknown directive %q; did you mean %q?", keyword, near)
	}
	return errAt(file, pos, "%s", msg)
}

func nearestKeyword(keyword string, names []string) string {
	for _, name := range names {
		if strings.EqualFold(name, keyword) || strings.HasPrefix(name, keyword) {
			return name
		}
	}
	return ""
}

func checkOnce(file string, seen map[string]Position, keyword string, pos Position) error {
	if first, ok := seen[keyword]; ok {
		return errAt(file, pos, "%s is already set at line %d", keyword, first.Line)
	}
	seen[keyword] = pos
	return nil
}

// apply folds one directive into the config. Each directive gets its own
// function: the shapes have nothing in common beyond the keyword, and a single
// switch grew into something nobody could read at a glance.
func apply(cfg *Config, file string, line *Line) error {
	if handle, ok := handlers[line.Keyword]; ok {
		return handle(cfg, file, line)
	}
	if blockOnly[line.Keyword] {
		return errAt(file, line.P, "%s", known[line.Keyword])
	}
	return nil
}

func applyProject(cfg *Config, file string, line *Line) error {
	if len(line.Args) != 1 {
		return arity(file, line)
	}
	cfg.Project = line.Args[0]
	return nil
}

func applyBuild(cfg *Config, file string, line *Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.Targets = append(cfg.Targets, line.Args...)
	return nil
}

func applyTags(cfg *Config, file string, line *Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.Tags = append(cfg.Tags, line.Args...)
	return nil
}

// applyPlugin reads one plugin pin.
//
// Four fields and no defaults: the hook says when it runs, the command what to
// run, the version what was asked for, and the digest what actually has to be
// on disk. Nothing here is optional, because a plugin that ran unpinned would
// be an unrecorded build input — the thing the whole contract exists to
// prevent.
func applyPlugin(cfg *Config, file string, line *Line) error {
	if len(line.Args) != 4 {
		return arity(file, line)
	}

	hook, command, version, digest := line.Args[0], line.Args[1], line.Args[2], line.Args[3]

	for _, existing := range cfg.Plugins {
		if existing.Hook == hook {
			return errAt(file, line.P, "a plugin for the %s hook is already set", hook)
		}
	}
	if !isSHA256(digest) {
		return errAt(file, line.P, "plugin %s: %q is not a sha256 digest", command, digest)
	}

	cfg.Plugins = append(cfg.Plugins, Plugin{
		Hook: hook, Command: command, Version: version, Digest: digest,
	})
	return nil
}

// isSHA256 reports whether s is a "sha256:" digest of the right length.
//
// Both the plugin and cgo directives pin an artifact this way, and a pin that
// parsed loosely in one of them would be a pin in name only.
func isSHA256(s string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(s, prefix) && len(s) == len(prefix)+64
}

// variantDirectives are what a variant may set.
//
// Deliberately three: a variant is the same source built differently, so it
// can change how it compiles and where it runs, and nothing else. Letting it
// set a project name or a tap would make it a second release configuration
// wearing the same file.
var variantDirectives = map[string]bool{"build": true, "tags": true, "cgo": true}

// applyVariant reads one variant block.
//
// The inner lines are applied to a throwaway config and the results lifted
// out, so a variant's `build` and `cgo` are parsed and validated by exactly
// the code that parses the release's own.
func applyVariant(cfg *Config, file string, b *Block) error {
	if len(b.Args) != 1 {
		return errAt(file, b.P, "%s", known["variant"])
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
	// `image ( base … )`.
	inner := &Config{Budgets: map[string]string{}}
	for _, line := range b.Lines {
		if len(line.Args) == 0 {
			continue
		}
		keyword, args := line.Args[0], line.Args[1:]
		if !variantDirectives[keyword] {
			return errAt(file, line.P,
				"a variant sets build, tags and cgo; %q belongs outside it", keyword)
		}
		if err := apply(inner, file, &Line{Keyword: keyword, Args: args, P: line.P}); err != nil {
			return err
		}
	}

	// Without its own targets a variant would build the whole matrix, which is
	// never what one is for: the GUI half of a repository exists precisely
	// because it does not run everywhere the CLI does.
	if len(inner.Targets) == 0 {
		return errAt(file, b.P, "variant %s must name the targets it builds for", name)
	}

	cfg.Variants = append(cfg.Variants, Variant{
		Name: name, Targets: inner.Targets, Tags: inner.Tags, CGo: inner.CGo,
	})
	return nil
}

// applyCGo reads the cgo settings.
//
// `cgo on` is the whole of it for most repositories: letsgo picks the compiler
// and records which one it used. Naming a version is for a repository that
// needs a particular zig, and a digest for one letsgo does not ship a pin for.
func applyCGo(cfg *Config, file string, line *Line) error {
	if cfg.CGo != nil {
		return errAt(file, line.P, "cgo is already set")
	}

	switch {
	case len(line.Args) == 1 && line.Args[0] == "on":
		cfg.CGo = &CGo{}
		return nil

	case len(line.Args) >= 2 && line.Args[0] == "zig":
		settings := &CGo{ZigVersion: line.Args[1]}
		switch len(line.Args) {
		case 2:
		case 3:
			digest := line.Args[2]
			if !isSHA256(digest) {
				return errAt(file, line.P, "cgo zig %s: %q is not a sha256 digest",
					settings.ZigVersion, digest)
			}
			settings.ZigDigest = digest
		default:
			return arity(file, line)
		}
		cfg.CGo = settings
		return nil

	default:
		return arity(file, line)
	}
}

func applyLDFlags(cfg *Config, file string, line *Line) error {
	if len(line.Args) == 0 {
		return arity(file, line)
	}
	cfg.LDFlags = append(cfg.LDFlags, line.Args...)
	return nil
}

func applyArchive(cfg *Config, file string, line *Line) error {
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
func applyModule(cfg *Config, file string, line *Line) error {
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
func applyVersion(cfg *Config, file string, line *Line) error {
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

func applyBudget(cfg *Config, file string, line *Line) error {
	if len(line.Args) != 2 {
		return arity(file, line)
	}
	if _, exists := cfg.Budgets[line.Args[0]]; exists {
		return errAt(file, line.P, "budget for %s is already set", line.Args[0])
	}
	cfg.Budgets[line.Args[0]] = line.Args[1]
	return nil
}

// imageSettings are the keyed forms of the image directive.
//
// One function each, for the same reason apply itself dispatches rather than
// switching: image carries more shapes than any other directive — a bare form,
// a bare reference, and a setting per key — and a single switch over all of
// them is past the point where anyone can read it at a glance.
var imageSettings = map[string]func(*Image, string, *Line) error{
	"base":   applyImageBase,
	"cmd":    applyImageCmd,
	"expose": applyImageExpose,
}

func applyImage(cfg *Config, file string, line *Line) error {
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

func applyImageBase(img *Image, file string, line *Line) error {
	if len(line.Args) != 2 {
		return arity(file, line)
	}
	if img.Base != "" {
		return errAt(file, line.P, "image base is already set")
	}
	img.Base = line.Args[1]
	return nil
}

func applyImageCmd(img *Image, file string, line *Line) error {
	if len(line.Args) < 2 {
		return arity(file, line)
	}
	if img.Cmd != nil {
		return errAt(file, line.P, "image cmd is already set")
	}
	img.Cmd = line.Args[1:]
	return nil
}

func applyImageExpose(img *Image, file string, line *Line) error {
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

func applyBrew(cfg *Config, file string, line *Line) error {
	if len(line.Args) != 1 {
		return arity(file, line)
	}
	if _, _, ok := strings.Cut(line.Args[0], "/"); !ok {
		return errAt(file, line.P, "brew tap %q must be in owner/repo form", line.Args[0])
	}
	cfg.BrewTap = line.Args[0]
	return nil
}

func applyRelease(cfg *Config, file string, line *Line) error {
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

		default:
			return errAt(file, line.P,
				"unknown release option %q; valid options are draft, prerelease", key)
		}
	}
	return nil
}

func arity(file string, line *Line) error {
	return errAt(file, line.P, "%s takes %s", line.Keyword, known[line.Keyword])
}
