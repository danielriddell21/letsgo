// Package feature holds the catalogue of letsgo's optional behaviours: what
// each one is called, whether a repository may turn it off or make it
// strict, and what it defaults to.
//
// The catalogue is the single source of truth for the `disable` and
// `require` directives, `letsgo features`, and the manifest's `features`
// record — so a feature that exists here but is never consulted by any of
// those is a bug, not a variant.
package feature

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

// Feature describes one entry in the catalogue.
type Feature struct {
	// Name is the identifier used in `disable`, `require`, the manifest and
	// `letsgo features` output.
	Name string
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
		Name: "reproducible", Kind: Integrity, Default: true,
		Summary: "the build is reproducible from source",
	},
	{
		Name: "source", Kind: Integrity, Default: true,
		Summary: "a source archive is published with the release",
	},
	{
		Name: "manifest", Kind: Integrity, Default: true,
		Summary: "letsgo.json records what was built and how",
	},
	{
		Name: "checksums", Kind: Integrity, Default: true,
		Summary: "SHA256SUMS lists every artifact's digest",
	},
	{
		Name: "tag-check", Kind: Integrity, Default: true,
		Summary: "the tag matches the version letsgo resolved",
	},
	{
		Name: "module-path", Kind: Integrity, Default: true,
		Summary: "the module path matches its major version",
	},

	{
		Name: "vulncheck", Kind: Gate, Default: true, Disable: true, Require: true,
		Summary: "govulncheck must find no reachable vulnerabilities",
	},
	{
		Name: "api-gate", Kind: Gate, Default: true, Disable: true, Require: true,
		Summary: "an incompatible API change needs a major version bump",
	},
	{
		Name: "budget", Kind: Gate, Default: false, Enable: "budget",
		Summary: "an artifact over its configured size fails the release",
	},

	{
		Name: "sbom", Kind: Output, Default: true, Disable: true,
		Summary: "a software bill of materials is published with the release",
	},
	{
		Name: "install-script", Kind: Output, Default: true, Disable: true, Require: true,
		Summary: "install.sh is generated for a GitHub release",
	},
	{
		Name: "changelog", Kind: Output, Default: true, Disable: true,
		Summary: "commits since the previous tag become the release body",
	},

	{
		Name: "proxy-warm", Kind: Publish, Default: true, Disable: true,
		Summary: "proxy.golang.org is primed after publishing",
	},
	{
		Name: "brew", Kind: Publish, Default: false, Enable: "brew",
		Summary: "a Homebrew formula is written to a tap",
	},
	{
		Name: "image", Kind: Publish, Default: false, Enable: "image",
		Summary: "a container image is built and published",
	},
}

// Lookup returns the feature named name, and whether it exists.
func Lookup(name string) (Feature, bool) {
	for _, f := range All {
		if f.Name == name {
			return f, true
		}
	}
	return Feature{}, false
}
