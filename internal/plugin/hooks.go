package plugin

// The types below are the wire format between core and a plugin. They are the
// whole contract: a plugin reads one JSON object from stdin and writes another
// to stdout, so it can be written in anything, and core can record the answer
// without understanding how it was reached.

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
