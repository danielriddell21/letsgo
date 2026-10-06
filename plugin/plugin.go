// Package plugin is the SDK a letsgo plugin is written against.
//
// The contract is deliberately small: letsgo runs a plugin with the hook's
// name as its only argument, writes one JSON object to its stdin, and reads
// one JSON object from its stdout. A plugin needs three things to answer a
// hook — the wire types for its input and output, and Main to read one and
// write the other — and this package is exactly those three things and
// nothing else. It has no dependency on the rest of letsgo, so a plugin
// importing it pulls in nothing beyond what it already needs. Its only
// dependency inside letsgo is modsyntax, the grammar of a Plugin's own config.
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
)

// Hook is a point in a release a plugin can answer for.
type Hook string

const (
	// HookLDFlags asks for extra -X assignments. The answer is recorded in the
	// artifact's ldflags, which verification already replays exactly.
	HookLDFlags Hook = "ldflags"

	// HookArchiveLayout asks which binaries share an archive. The answer is
	// recorded as the artifact's binaries.
	HookArchiveLayout Hook = "archive-layout"

	// HookTapFiles asks what else belongs in the Homebrew tap beside the
	// formula, a cask most often. Core writes what comes back and records it
	// in the manifest; it never changes a released byte, so verification has
	// nothing to replay.
	HookTapFiles Hook = "tap-files"
)

// Hooks is the closed set, in the order they run.
var Hooks = []Hook{HookLDFlags, HookArchiveLayout, HookTapFiles}

// Valid reports whether a hook is one letsgo knows.
func (h Hook) Valid() bool {
	for _, known := range Hooks {
		if h == known {
			return true
		}
	}
	return false
}

// LDFlagsInput is what the ldflags hook is told.
type LDFlagsInput struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Commit  string `json:"commit"`

	// Date is the commit's timestamp, RFC 3339. Never the clock: a plugin that
	// read the time would make the release unreproducible in a way core could
	// not see.
	Date string `json:"date"`

	// Module is the module path being built.
	Module string `json:"module"`

	// Targets are the "goos/goarch" pairs the release builds.
	Targets []string `json:"targets"`

	// ConfigDir is where this plugin's own config lives, ".letsgo" beside
	// letsgo.mod, so a plugin never has to guess where it runs from.
	ConfigDir string `json:"config_dir"`
}

// LDFlagsOutput is what it answers: extra linker arguments, appended after the
// version injection core does itself.
//
// Whatever comes back is written into the artifact's recorded ldflags, so a
// rebuild replays these exactly and never runs the plugin again.
type LDFlagsOutput struct {
	LDFlags []string `json:"ldflags"`
}

// ArchiveLayoutInput is what the archive-layout hook is told.
type ArchiveLayoutInput struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Module  string `json:"module"`

	// Commands are every main package the module builds.
	Commands []InputCommand `json:"commands"`

	Targets []string `json:"targets"`

	// ConfigDir is where this plugin's own config lives, ".letsgo" beside
	// letsgo.mod, so a plugin never has to guess where it runs from.
	ConfigDir string `json:"config_dir"`
}

// InputCommand is one main package offered to the layout hook.
type InputCommand struct {
	// Binary is the executable's name, and Package its import path relative to
	// the module, e.g. "./cmd/foo".
	Binary  string `json:"binary"`
	Package string `json:"package"`
}

// ArchiveLayoutOutput is what it answers: which binaries share an archive.
type ArchiveLayoutOutput struct {
	Archives []OutputArchive `json:"archives"`
}

// OutputArchive is one archive the release should produce.
type OutputArchive struct {
	// Name is the archive's base name, without version, platform or extension.
	Name string `json:"name"`

	// Binaries are the executables inside it, named as the input named them.
	Binaries []string `json:"binaries"`
}

// TapFilesInput is what the tap-files hook is told: everything a formula
// writer already has, so a plugin never rebuilds a download URL or asks the
// forge a question core has already answered.
type TapFilesInput struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Tag     string `json:"tag"`

	// Repo and Tap are "owner/name".
	Repo string `json:"repo"`
	Tap  string `json:"tap"`

	// Description, License and Homepage come from the repository, not a
	// flag: the same facts a formula is written with.
	Description string `json:"description,omitempty"`
	License     string `json:"license,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Caveats     string `json:"caveats,omitempty"`

	Artifacts []TapArtifact `json:"artifacts"`

	// ConfigDir is where this plugin's own config lives, ".letsgo" beside
	// letsgo.mod, so a plugin never has to guess where it runs from.
	ConfigDir string `json:"config_dir"`
}

// TapArtifact is one archive the plugin can point a file at.
type TapArtifact struct {
	Archive string `json:"archive"`

	// Variant is the group name a non-default build was made for, empty for
	// the release's ordinary archives. A cask usually exists for a variant a
	// formula does not carry, such as a windowed build.
	Variant string `json:"variant,omitempty"`

	OS     string `json:"os"`
	Arch   string `json:"arch"`
	SHA256 string `json:"sha256"`

	// URL is already built from core's own escaping, so a plugin never
	// reimplements it and never disagrees with the formula about a tag
	// containing a slash.
	URL string `json:"url"`

	Binaries []string `json:"binaries"`
}

// TapFilesOutput is what the hook answers: files to write into the tap,
// beside the formula.
type TapFilesOutput struct {
	Files []TapFile `json:"files"`
}

// TapFile is one file to write into the tap.
type TapFile struct {
	// Path is relative to the tap's root, and must stay under Casks/: the
	// plugin decides what goes in the tap, not where in the repository it may
	// write.
	Path string `json:"path"`

	Content string `json:"content"`
}

// Main runs a hook and exits: the whole of a plugin's main function.
//
//	func main() { plugin.Main(plugin.HookArchiveLayout, multi.Layout) }
//
// It checks that the hook letsgo invoked is the one answer answers, decodes
// stdin into In, calls answer, and encodes its result to stdout. An error —
// from the hook mismatch, decoding, answer itself, or encoding — is printed to
// stderr and exits the process non-zero.
func Main[In, Out any](hook Hook, answer func(In) (Out, error)) {
	if err := run(hook, answer); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[0], err)
		os.Exit(1)
	}
}

func run[In, Out any](hook Hook, answer func(In) (Out, error)) error {
	// letsgo passes the hook's name, so one binary could answer several. A
	// plugin asked for a hook it does not implement says so rather than
	// guessing, because guessing would mean answering the wrong question with
	// a plausible-looking result.
	if len(os.Args) != 2 {
		return fmt.Errorf("expected one argument, the hook to answer")
	}
	if os.Args[1] != string(hook) {
		return fmt.Errorf("this plugin answers the %s hook, not %s", hook, os.Args[1])
	}

	var in In
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return fmt.Errorf("reading the hook's input: %w", err)
	}

	out, err := answer(in)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		return fmt.Errorf("writing the hook's answer: %w", err)
	}
	return nil
}
