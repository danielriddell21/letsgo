package config

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/danielriddell21/letsgo/internal/feature"
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

	// Tags are build tags. They change the compiled bytes deterministically
	// and are pinned by the commit like any other config, so they cost the
	// reproducibility claim nothing.
	Tags []string

	// Variants are additional builds of the same commands, differing by tags
	// and target. A repository with a headless CLI and a GUI build behind a
	// tag has two products from one source, and they ship side by side.
	Variants []Variant

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

	// BudgetPos is where each entry in Budgets was written, so a check raised
	// against a bad budget can point at the line that caused it rather than
	// merely naming the target.
	BudgetPos map[string]Position

	// Pos is where each directive was first written, keyed by its name, so a
	// check raised against what a directive asked for can point at its line.
	// A directive that repeats per subject has a key per subject — "plugin
	// <hook>", "disable <feature>", "require <feature>".
	Pos map[string]Position

	// Image describes the container image to publish. Nil means none: a
	// release that creates a package in a registry should be something the
	// repository asked for.
	Image *Image

	// BrewTap is an "owner/repo" Homebrew tap to publish a formula to.
	BrewTap string

	// BrewCaveats is what the formula tells someone after installing. The
	// one piece of a formula that cannot be derived from the release or the
	// repository description: it says what this program needs of the machine
	// it landed on, which only the author knows.
	BrewCaveats string

	// Prerelease is "auto", "true" or "false".
	Prerelease string

	// Draft creates the release without publishing it.
	Draft bool

	// Latest is "auto", "true" or "false". GitHub has one "latest" release
	// per repository; auto claims it for a root module and defers for a
	// scoped one (see discover.Scope), rather than fight whichever module
	// released last for the badge.
	Latest string

	// Disabled are the features this repository turned off, by name from the
	// feature catalogue (internal/feature).
	Disabled []string

	// Required are the features whose Skip becomes a Fail, by name from the
	// feature catalogue.
	Required []string
}

// Plugin is one external program invoked at a named hook.
type Plugin struct {
	Hook    string
	Command string
	Version string
	Digest  string
}

// Variant is one additional build of the module's commands.
//
// A variant names its own targets rather than inheriting the release's,
// because the reason to have one is usually that it does not build
// everywhere: a GUI build needs a windowing system, and the point is to ship
// it only where it works.
type Variant struct {
	// Name suffixes the archive, as in "gambit-gui_1.0.0_darwin_arm64".
	Name string

	Targets []string
	Tags    []string
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
	"variant": "a variant <name> ( ... ) block setting build and tags",
	"plugin":  "plugin <hook> <command> <version> sha256:<digest>",
	"ldflags": "ldflags <flag>...",
	"version": "version <symbol>, or version commit|date <symbol>",
	"archive": "archive <file>... or an archive ( ... ) block",
	"budget":  "budget <goos/goarch> <size>",
	"image":   "image, image <reference>, image base <ref>, image cmd <arg>..., or image expose <port>...",
	"brew":    "brew <owner/tap-repo>, or brew caveats <text>",
	"release": "release <key=value>...",
	"disable": "disable <feature>...",
	"require": "require <feature>...",
}

// blockOnly names the directives that exist only as a block. Written down so
// that using one as a plain line says so, rather than parsing and quietly
// doing nothing.
var blockOnly = map[string]bool{"variant": true}

// docs is a one-line explanation per directive, for an editor's hover text.
// Kept separate from known for the same reason handlers is: arity() reads
// known, and every directive here already has an entry there.
var docs = map[string]string{
	"project": "Overrides the project name derived from the module path.",
	"module":  "The directory, relative to the repository root, holding the go.mod to build.",
	"build":   "Overrides the default build matrix of goos/goarch targets.",
	"tags":    "Build tags applied to every build.",
	"variant": "An additional build of the module's commands, with its own targets and tags.",
	"plugin":  "Pins an external program at a hook, by digest.",
	"ldflags": "Linker flags appended to the defaults.",
	"version": "Names the variables version metadata is injected into.",
	"archive": "Extra files to include in each archive.",
	"budget":  "Caps a target's binary size; over it fails the release.",
	"image":   "Describes the container image a release publishes.",
	"brew":    "The Homebrew tap a formula is published to, or its caveats text.",
	"release": "Sets a release option by key=value.",
	"disable": "Turns off an optional feature by name.",
	"require": "Turns a feature's missing tool into a failure instead of a skip.",
}

// Doc returns a directive's usage and documentation, for hover text. ok is
// false for an unknown keyword.
func Doc(keyword string) (usage, doc string, ok bool) {
	usage, ok = known[keyword]
	if !ok {
		return "", "", false
	}
	return usage, docs[keyword], true
}

// Directives lists every known directive name, sorted.
func Directives() []string {
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// handlers folds each directive into the config.
var handlers = map[string]func(cfg *Config, file string, line *Line) error{
	"project": applyProject,
	"module":  applyModule,
	"build":   applyBuild,
	"tags":    applyTags,
	"plugin":  applyPlugin,
	"ldflags": applyLDFlags,
	"version": applyVersion,
	"archive": applyArchive,
	"budget":  applyBudget,
	"image":   applyImage,
	"brew":    applyBrew,
	"release": applyRelease,
	"disable": applyDisable,
	"require": applyRequire,
}

// mark records where a directive was written. The first occurrence wins: a
// repeated list directive is still one thing to point at, and the first line
// is where a reader starts looking.
func (c *Config) mark(key string, pos Position) {
	if c.Pos == nil {
		c.Pos = map[string]Position{}
	}
	if _, ok := c.Pos[key]; !ok {
		c.Pos[key] = pos
	}
}

// Decode interprets a parsed file.
func Decode(f *File) (*Config, error) {
	cfg := &Config{Budgets: map[string]string{}, BudgetPos: map[string]Position{}}
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
	cfg.mark(b.Keyword, b.P)

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
	cfg.mark(line.Keyword, line.P)
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
	case "project", "module":
		return true
	}
	return false
}

func checkKnown(file, keyword string, pos Position) error {
	if _, ok := known[keyword]; ok {
		return nil
	}
	if _, ok := globalKnown[keyword]; ok {
		return errAt(file, pos, "%s belongs in the global config, not letsgo.mod", keyword)
	}

	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)

	return unknownName(file, pos, "directive", keyword, names)
}

// nearestKeyword finds the name closest to keyword, for a did-you-mean
// suggestion. A prefix or a case difference is caught outright; anything else
// falls back to edit distance, so a transposed pair of letters (sbmo for
// sbom) still gets a suggestion rather than the full list.
func nearestKeyword(keyword string, names []string) string {
	for _, name := range names {
		if strings.EqualFold(name, keyword) || strings.HasPrefix(name, keyword) {
			return name
		}
	}

	best, bestDist := "", -1
	for _, name := range names {
		d := levenshtein(strings.ToLower(keyword), strings.ToLower(name))
		if bestDist == -1 || d < bestDist {
			best, bestDist = name, d
		}
	}

	// Worth suggesting only when the typo is close: past this, a guess is as
	// likely to be wrong as right, and the full list serves the reader better.
	if best != "" && bestDist <= (len(keyword)+1)/2 {
		return best
	}
	return ""
}

// levenshtein is the edit distance between two strings: the fewest
// insertions, deletions and substitutions that turn one into the other.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
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
	// `image ( base ... )`.
	inner := &Config{Budgets: map[string]string{}, BudgetPos: map[string]Position{}}
	for _, line := range b.Lines {
		if len(line.Args) == 0 {
			continue
		}
		keyword, args := line.Args[0], line.Args[1:]
		if !variantDirectives[keyword] {
			return errAt(file, line.P,
				"a variant sets build and tags; %q belongs outside it", keyword)
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
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return errAt(file, line.P, "plugin %s: %q is not a sha256 digest", command, digest)
	}

	cfg.mark("plugin "+hook, line.P)
	cfg.Plugins = append(cfg.Plugins, Plugin{
		Hook: hook, Command: command, Version: version, Digest: digest,
	})
	return nil
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
	cfg.BudgetPos[line.Args[0]] = line.P
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

// applyBrew reads where the formula goes, and what it says afterwards.
//
// The bare form names the tap, which is the case worth being short; caveats
// are keyed, following the same shape as the version and image directives.
// Repeating either is rejected here rather than by the scalar check, because
// the two forms are separate settings sharing one keyword.
func applyBrew(cfg *Config, file string, line *Line) error {
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
func applyDisable(cfg *Config, file string, line *Line) error {
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
func applyRequire(cfg *Config, file string, line *Line) error {
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
func unknownFeature(file string, pos Position, name string) error {
	names := make([]string, len(feature.All))
	for i, f := range feature.All {
		names[i] = f.Name
	}
	sort.Strings(names)
	return unknownName(file, pos, "feature", name, names)
}

// unknownName reports a word that is not one of names, suggesting the nearest
// when there is one. The suggestion rides on the error as data, so an editor
// need not read it back out of the message.
func unknownName(file string, pos Position, what, word string, names []string) error {
	if near := nearestKeyword(word, names); near != "" {
		return &SyntaxError{
			File: file, Pos: pos, Wrong: word, Suggest: near,
			Msg: fmt.Sprintf("unknown %s %q; did you mean %q?", what, word, near),
		}
	}
	return errAt(file, pos, "unknown %s %q; valid %ss are %s", what, word, what, strings.Join(names, ", "))
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func arity(file string, line *Line) error {
	return errAt(file, line.P, "%s takes %s", line.Keyword, known[line.Keyword])
}
