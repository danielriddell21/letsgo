// Package feature holds the catalogue of letsgo's optional behaviours: what
// each one is called, whether a repository may turn it off or make it
// strict, and what it defaults to.
//
// The catalogue is the single source of truth for the `disable` and
// `require` directives, `letsgo features`, and the manifest's `features`
// record — so a feature that exists here but is never consulted by any of
// those is a bug, not a variant.
package feature

import "sort"

// Kind groups a feature by what it protects, which is what a repository is
// agreeing to give up when it turns one off.
type Kind int

const (
	// Integrity features are what letsgo is. None of them can be disabled.
	Integrity Kind = iota
	// Gate features can fail a release. Disabling one skips the check;
	// requiring one turns a missing tool into a failure instead of a skip.
	Gate
	// Output features add something to a release's assets or its body.
	Output
	// Publish features write somewhere beyond the release itself.
	Publish
)

func (k Kind) String() string {
	switch k {
	case Integrity:
		return "integrity"
	case Gate:
		return "gate"
	case Output:
		return "output"
	case Publish:
		return "publish"
	default:
		return "unknown"
	}
}

// Name identifies one catalogue entry. Code that consults a feature uses
// these constants, never a quoted string, so a retired entry fails to compile.
type Name string

const (
	Reproducible  Name = "reproducible"
	Source        Name = "source"
	Manifest      Name = "manifest"
	Checksums     Name = "checksums"
	TagCheck      Name = "tag-check"
	ModulePath    Name = "module-path"
	Vulncheck     Name = "vulncheck"
	APIGate       Name = "api-gate"
	Sumdb         Name = "sumdb"
	Budget        Name = "budget"
	SBOM          Name = "sbom"
	InstallScript Name = "install-script"
	Changelog     Name = "changelog"
	DiffNotes     Name = "diff-notes"
	Randomart     Name = "randomart"
	ProxyWarm     Name = "proxy-warm"
	Brew          Name = "brew"
	Image         Name = "image"
)

// Feature describes one entry in the catalogue.
type Feature struct {
	// Name is the identifier used in `disable`, `require`, the manifest and
	// `letsgo features` output.
	Name Name
	Kind Kind

	// Default is whether the feature is on with no configuration at all.
	Default bool

	// Enable is the directive that turns on a feature whose Default is
	// false, for example "brew" or "image". It is empty for a feature
	// that is on by default or that has no directive of its own.
	Enable string

	// Disable reports whether `disable <name>` is allowed.
	Disable bool
	// Require reports whether `require <name>` is allowed.
	Require bool

	// Summary is one line describing what the feature does, for
	// `letsgo features` and directive documentation.
	Summary string
}

// All is the closed set of features letsgo knows about, in the order they
// are documented: integrity first, then gates, outputs and publish targets.
var All = []Feature{
	{
		Name: Reproducible, Kind: Integrity, Default: true,
		Summary: "the build is reproducible from source",
	},
	{
		Name: Source, Kind: Integrity, Default: true,
		Summary: "a source archive is published with the release",
	},
	{
		Name: Manifest, Kind: Integrity, Default: true,
		Summary: "letsgo.json records what was built and how",
	},
	{
		Name: Checksums, Kind: Integrity, Default: true,
		Summary: "SHA256SUMS lists every artifact's digest",
	},
	{
		Name: TagCheck, Kind: Integrity, Default: true,
		Summary: "the tag matches the version letsgo resolved",
	},
	{
		Name: ModulePath, Kind: Integrity, Default: true,
		Summary: "the module path matches its major version",
	},

	{
		Name: Vulncheck, Kind: Gate, Default: true, Disable: true, Require: true,
		Summary: "govulncheck must find no reachable vulnerabilities",
	},
	{
		Name: APIGate, Kind: Gate, Default: true, Disable: true, Require: true,
		Summary: "an incompatible API change needs a major version bump",
	},
	{
		Name: Sumdb, Kind: Gate, Default: true, Disable: true, Require: true,
		Summary: "sum.golang.org must agree with the source archive before assets are published",
	},
	{
		Name: Budget, Kind: Gate, Default: false, Enable: "budget",
		Summary: "an artifact over its configured size fails the release",
	},

	{
		Name: SBOM, Kind: Output, Default: true, Disable: true, Require: true,
		Summary: "a software bill of materials is published with the release",
	},
	{
		Name: InstallScript, Kind: Output, Default: true, Disable: true, Require: true,
		Summary: "install.sh is generated for a GitHub release",
	},
	{
		Name: Changelog, Kind: Output, Default: true, Disable: true, Require: true,
		Summary: "commits since the previous tag become the release body",
	},
	{
		Name: DiffNotes, Kind: Output, Default: true, Disable: true, Require: true,
		Summary: "a collapsed \"what shipped\" section compares this release's manifest against the previous one",
	},
	{
		Summary: "a collapsed fingerprint of the manifest is added to the release notes",
		Name:    Randomart, Require: true, Disable: true, Default: true, Kind: Output,
	},

	{
		Name: ProxyWarm, Kind: Publish, Default: true, Disable: true,
		Summary: "proxy.golang.org is primed before the release is published",
	},
	{
		Name: Brew, Kind: Publish, Default: false, Enable: "brew",
		Summary: "a Homebrew formula is written to a tap",
	},
	{
		Name: Image, Kind: Publish, Default: false, Enable: "image",
		Summary: "a container image is built and published",
	},
}

// Lookup returns the feature named name, and whether it exists.
func Lookup(name string) (Feature, bool) {
	for _, f := range All {
		if string(f.Name) == name {
			return f, true
		}
	}
	return Feature{}, false
}

// Set is which catalogue features are disabled for a release, however that
// was decided — a `disable` directive, or a one-run flag such as
// `--no-proxy-warm` that means the same thing.
type Set map[Name]bool

// Resolve builds a Set from the names a release disabled. Repeats are
// harmless: this is a set, not a log of how each name was said.
func Resolve(disabled []string) Set {
	s := make(Set, len(disabled))
	for _, name := range disabled {
		s[Name(name)] = true
	}
	return s
}

// On reports whether a feature is on. Only meaningful for a feature whose
// Default is true: one that defaults off is on only because its own
// directive is present, which a Set knows nothing about.
func (s Set) On(name Name) bool {
	return !s[name]
}

// Disabled lists every disabled name, sorted, for recording in the manifest
// and printing in a report.
func (s Set) Disabled() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return names
}
