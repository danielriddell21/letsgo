package config

import (
	"github.com/danielriddell21/letsgo/modsyntax"
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
	BudgetPos map[string]modsyntax.Position

	// Pos is where each directive was first written, keyed by its name, so a
	// check raised against what a directive asked for can point at its line.
	// A directive that repeats per subject has a key per subject — "plugin
	// <hook>", "disable <feature>", "require <feature>".
	Pos map[string]modsyntax.Position

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
