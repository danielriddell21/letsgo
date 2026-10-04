package config

import (
	"sort"

	"github.com/danielriddell21/letsgo/modsyntax"
)

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

// directiveSet is a closed vocabulary of directives: each name's usage, for
// arity errors, and its one-line doc, for hover text. letsgo.mod and the
// global config each have one, so lookup, listing and the errors a wrong
// directive produces are written once.
//
// It holds no handlers: arity() reads the usage and every handler calls
// arity(), which would be an initialisation cycle if the two shared a table.
type directiveSet struct {
	usage map[string]string
	doc   map[string]string
}

// modDirectives is letsgo.mod's vocabulary.
var modDirectives = directiveSet{usage: known, doc: docs}

func (s directiveSet) has(keyword string) bool {
	_, ok := s.usage[keyword]
	return ok
}

// lookup returns a directive's usage and documentation. ok is false for an
// unknown keyword.
func (s directiveSet) lookup(keyword string) (usage, doc string, ok bool) {
	usage, ok = s.usage[keyword]
	if !ok {
		return "", "", false
	}
	return usage, s.doc[keyword], true
}

// names lists every directive name, sorted.
func (s directiveSet) names() []string {
	names := make([]string, 0, len(s.usage))
	for name := range s.usage {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unknown is the error for a keyword the set does not contain, with the
// nearest name as a suggestion.
func (s directiveSet) unknown(file, keyword string, pos modsyntax.Position) error {
	return unknownName(file, pos, "directive", keyword, s.names())
}

// arity is the error for a directive given the wrong arguments.
func (s directiveSet) arity(file string, line *modsyntax.Line) error {
	return errAt(file, line.P, "%s takes %s", line.Keyword, s.usage[line.Keyword])
}

// Doc returns a letsgo.mod directive's usage and documentation, for hover
// text. ok is false for an unknown keyword.
func Doc(keyword string) (usage, doc string, ok bool) {
	return modDirectives.lookup(keyword)
}

// Directives lists every letsgo.mod directive name, sorted.
func Directives() []string { return modDirectives.names() }

// handlers folds each directive into the config.
var handlers = map[string]func(cfg *Config, file string, line *modsyntax.Line) error{
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
