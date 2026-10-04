// Package plan resolves a release without performing one.
//
// Everything that can be decided cheaply is decided here: configuration is
// read, defaults are inferred, the build matrix is validated, and the checks
// that do not need the network are run. The result is a complete description
// of what a release would produce.
//
// The ordering is the point. A release that is going to fail should fail in
// about two seconds, before a cross-compile matrix has been built, rather than
// after.
package plan

import (
	"context"
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/modsyntax"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// pluginConfigDir is where a plugin's own config lives, beside config.FileName.
const pluginConfigDir = ".letsgo"

// disabledByConfig is the detail of a check its feature switched off.
const disabledByConfig = "disabled by config"

// Options control how a plan is resolved.
type Options struct {
	// Dir is any directory inside the module.
	Dir string

	// Global is the machine's configuration, read once by the caller and
	// handed down. Nil reads it from the file, which is what a command does.
	Global *config.Global

	// GlobalErr is why the caller could not read the machine's configuration.
	// With a nil Global, the plan reports it as a failed check instead of
	// reading the file a second time to find out.
	GlobalErr error

	// Snapshot builds an untagged working version.
	Snapshot bool

	// AllowDirty permits an unclean worktree. Never valid for a real release.
	AllowDirty bool

	// Publish adds the checks a release needs but a local build does not:
	// a forge to publish to, and a token permitted to write there.
	Publish bool

	// Credentials are the tokens the release is written with, resolved once
	// by the caller (see internal/credential). plan never looks a token up: it
	// checks the ones it is given, and reports where each came from.
	Credentials credential.Set

	// NewClient builds the forge client for a token, so the caller decides
	// where the forge is. Nil means the real one.
	NewClient func(token string) *github.Client

	// Analyse runs the gates that need program analysis rather than
	// inspection. They take seconds rather than milliseconds, so a bare plan
	// leaves them out and a release does not: the promise that a plan fails in
	// about two seconds is about configuration mistakes, and paying for an
	// analysis on every iteration would trade that away for little.
	Analyse bool

	// AllowVulnerable publishes despite reachable vulnerabilities, recording
	// which were accepted rather than hiding them.
	AllowVulnerable bool

	// AllowBreaking publishes an incompatible API change without a major
	// version bump.
	AllowBreaking bool

	// DisableProxyWarm skips priming the module proxy for this run only. It
	// means the same thing as `disable proxy-warm` in letsgo.mod, and is
	// recorded in the plan and the manifest the same way, so `--no-proxy-warm`
	// is indistinguishable from a repository that always disables it.
	DisableProxyWarm bool

	// Tag pins the version tag to resolve, overriding the "several tags on
	// HEAD" failure. Empty means the ordinary rule: whichever one tag is on
	// HEAD. `letsgo promote` is the one caller that needs this — it tags an
	// RC's commit with the stable version to rebuild there, and that commit
	// already carries the RC's own tag, so two qualifying tags on HEAD is
	// expected rather than a mistake.
	Tag string
}

// Status is the outcome of one check.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	Warn Status = "warn"
	Skip Status = "skip"
)

// Check is one gate result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`

	// Pos is where in the config file this check's directive was written,
	// when it was raised against one — nil for a check with no single line to
	// blame (a missing tool, a registry response). An editor turns it into a
	// diagnostic squiggle under that line.
	Pos *Pos `json:"pos,omitempty"`
}

// Pos names a location in a config file.
type Pos struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// Source records where a resolved value came from, for plan --explain.
type Source struct {
	Field string `json:"field"`
	Value string `json:"value"`
	From  string `json:"from"`
}

// Artifact is one archive a release would produce.
type Artifact struct {
	Name   string
	Target gobuild.Target
	Format archive.Format

	// Commands are the binaries inside it. One is the ordinary case; a module
	// whose product is a collection of tools ships them together.
	Commands []discover.MainPackage
}

// Group is one archive's contents, before targets multiply it.
//
// This is the seam a layout plugin decides: core knows how to carry several
// binaries in one archive, and what goes where is a question core answers by
// default and a plugin can answer instead.
type Group struct {
	// Name is the archive's base name, without version, platform or
	// extension.
	Name string

	Commands []discover.MainPackage

	// Targets and Tags are the group's own. The release's for a group that
	// declared none; a variant's where it did, because a variant exists
	// precisely to be compiled differently.
	Targets []gobuild.Target
	Tags    []string

	// Variant names the variant this group came from, empty for the default
	// build. It suffixes the archive.
	Variant string
}

// Program is the name the binary inside this group's archive carries.
//
// A variant's suffix tells two archives apart, which is all it is for.
// Carrying it into the binary would rename the program, and the command
// somebody types should not depend on which of two builds they installed.
func (g Group) Program() string {
	if g.Variant == "" {
		return g.Name
	}
	return strings.TrimSuffix(g.Name, "-"+g.Variant)
}

// Plan is a fully resolved, unexecuted release.
type Plan struct {
	// Module is the module that gets built, which the `module` directive can
	// move into a subdirectory. RootDir is the repository itself: where git
	// runs, where letsgo.mod lives, what the source archive covers, and what
	// archive files resolve against.
	//
	// They are the same directory in every repository that does not say
	// otherwise, and the distinction only exists because a nested module is a
	// fact about the layout rather than a different project: a release of
	// ./web still ships the repository's README, and still has to publish
	// source that can rebuild it.
	Module  discover.Module
	RootDir string

	// root is the module beside letsgo.mod, kept for the names that describe
	// the repository rather than the module being built.
	root discover.Module

	Git   git.State
	Scope discover.Scope

	// GitBin is the git command resolved from the environment and global
	// config, handed to everything that reads the repository.
	GitBin string

	Repo       discover.Repo
	HasRepo    bool
	Config     *config.Config
	ConfigPath string

	// Features are the departures from the defaults this release resolved,
	// from letsgo.mod's `disable` directive and any one-run flag that means
	// the same thing.
	Features feature.Set

	// Required are the features whose Skip this release turns into a Fail,
	// from letsgo.mod's `require` directive.
	Required []string

	Project  string
	Version  string
	Tag      string
	Snapshot bool

	Targets   []gobuild.Target
	Commands  []discover.MainPackage
	LDFlags   []string
	Files     []string
	Artifacts []Artifact

	// Groups are the archives the release produces, each holding one or more
	// commands.
	Groups []Group

	// Plugins are the pinned programs this release runs, by hook.
	Plugins map[plugin.Hook]plugin.Plugin

	// Tags are build tags passed to the compiler.
	Tags []string

	// Symbols names the variables the version metadata is injected into,
	// fully qualified. It defaults to the conventional main.version,
	// main.commit and main.date.
	Symbols VersionSymbols

	// Budgets caps each target's binary size. Parsed here so that a malformed
	// size is reported with every other planning problem, rather than after a
	// full matrix has been compiled.
	Budgets map[string]bytesize.Size

	// Tap is the Homebrew repository a formula is published to. Zero when no
	// tap is configured.
	Tap github.Repo

	// Image is the container image a release publishes. Nil when none is
	// configured, which is the default.
	Image *ImageTarget

	// Global is the machine configuration this plan was resolved under, so
	// what is done with the plan reads the same settings the plan reported.
	Global *config.Global

	// GoBin is the go command resolved from the environment and global
	// config, empty when none was found.
	GoBin string

	// Proxy is the module proxy this release would warm, resolved from
	// GOPROXY, the global config, or the fixed default.
	Proxy string

	Checks  []Check
	Sources []Source

	// APIChanges is the exported API delta against the previous release. It
	// feeds the changelog as well as the gate: the diff describes what the
	// code did, where a commit message describes what someone meant.
	APIChanges []gate.Change
}

// OK reports whether every check passed.
func (p *Plan) OK() bool {
	for _, c := range p.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

func (p *Plan) add(name string, status Status, format string, args ...any) {
	p.Checks = append(p.Checks, Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
}

// addAt is add for a check raised against a specific line in the config
// file: an editor places it as a diagnostic there instead of only listing it
// in the plan report.
func (p *Plan) addAt(pos *Pos, name string, status Status, format string, args ...any) {
	p.Checks = append(p.Checks,
		Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...), Pos: pos})
}

// configPos turns a directive's position within letsgo.mod into a Check's
// Pos, nil when there is nothing to point at (no config file, or a
// directive that recorded no position for this value).
// posOf is configPos for the directive written under key (see config.Config.Pos).
func (p *Plan) posOf(key string) *Pos {
	return p.configPos(p.Config.Pos[key])
}

func (p *Plan) configPos(cp modsyntax.Position) *Pos {
	if cp.Line == 0 || p.ConfigPath == "" {
		return nil
	}
	return &Pos{File: p.ConfigPath, Line: cp.Line, Col: cp.Col}
}

func (p *Plan) note(field, value, from string) {
	p.Sources = append(p.Sources, Source{Field: field, Value: value, From: from})
}

// Resolve builds a plan. It returns an error only when the repository cannot
// be inspected at all; ordinary problems become failed checks, so that one run
// reports every issue rather than only the first.
func Resolve(ctx context.Context, opts Options) (*Plan, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}

	machine := resolveMachine(opts.Global, opts.GlobalErr)
	if machine.gitErr != nil {
		return nil, machine.gitErr
	}

	loc, err := discover.Locate(ctx, machine.gitBin, dir)
	if err != nil {
		return nil, err
	}
	root, scope := loc.Module, loc.Scope

	p := &Plan{Module: root, RootDir: root.Dir, root: root, Git: loc.Git, Scope: scope, Snapshot: opts.Snapshot}
	p.note("commit", loc.Git.Commit, "git HEAD")
	p.note("commit time", loc.Git.CommitTime.Format("2006-01-02T15:04:05Z"), "git committer timestamp")
	if scope.Prefix != "" {
		p.note("scope", scope.Dir, "the module's own directory")
	}

	if loc.HasRepo() {
		p.Repo, p.HasRepo = loc.Repo, true
		p.note("repository", loc.Repo.String(), "git remote origin")
	}

	p.recordMachine(machine)

	// The config is read before the module is settled, because it is what can
	// move it: `module web` says the go.mod to build is not the one beside
	// letsgo.mod.
	p.loadConfig(root.Dir)
	p.resolveFeatures(opts)
	p.resolvePlugins()
	p.checkPluginConfigFiles()
	p.resolveModule()
	p.checkReplace()
	p.resolveProject()
	p.resolveVersion(ctx, opts)
	p.checkWorktree(opts)
	p.resolveTargets(ctx)
	p.resolveBudgets()
	p.resolveCommands(ctx)
	p.resolveFiles(ctx)
	p.resolveArtifacts(ctx)
	p.checkInstallScriptRequired()
	p.checkNotesRequired()

	p.resolveTap()
	p.resolveImage(ctx)
	p.hintPlugins()

	if opts.Publish {
		p.checkForge(ctx, opts)
		p.checkSumdb()
	}
	if opts.Analyse {
		p.checkVulnerabilities(ctx, opts)
		p.checkAPICompatibility(ctx, opts)
	}

	return p, nil
}
