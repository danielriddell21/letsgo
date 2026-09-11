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
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// ConfigFile is the optional configuration file letsgo reads.
const ConfigFile = "letsgo.mod"

// Options control how a plan is resolved.
type Options struct {
	// Dir is any directory inside the module.
	Dir string

	// Snapshot builds an untagged working version.
	Snapshot bool

	// AllowDirty permits an unclean worktree. Never valid for a real release.
	AllowDirty bool
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
	Name   string
	Status Status
	Detail string
}

// Source records where a resolved value came from, for plan --explain.
type Source struct {
	Field string
	Value string
	From  string
}

// Artifact is one archive a release would produce.
type Artifact struct {
	Name    string
	Target  gobuild.Target
	Command discover.MainPackage
	Format  archive.Format
}

// Plan is a fully resolved, unexecuted release.
type Plan struct {
	Module     discover.Module
	Git        discover.Git
	Repo       discover.Repo
	HasRepo    bool
	Config     *config.Config
	ConfigPath string

	Project  string
	Version  string
	Tag      string
	Snapshot bool

	Targets   []gobuild.Target
	Commands  []discover.MainPackage
	LDFlags   []string
	Files     []string
	Artifacts []Artifact

	Checks  []Check
	Sources []Source
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

	module, err := discover.FindModule(dir)
	if err != nil {
		return nil, err
	}

	git, err := discover.FindGit(ctx, module.Dir)
	if err != nil {
		return nil, err
	}

	p := &Plan{Module: module, Git: git, Snapshot: opts.Snapshot}
	p.note("module", module.Path, "go.mod")
	p.note("commit", git.Commit, "git HEAD")
	p.note("commit time", git.CommitTime.Format("2006-01-02T15:04:05Z"), "git committer timestamp")

	if repo, err := discover.FindRepo(ctx, module.Dir); err == nil {
		p.Repo, p.HasRepo = repo, true
		p.note("repository", repo.String(), "git remote origin")
	}

	p.loadConfig(module.Dir)
	p.resolveProject()
	p.resolveVersion(ctx)
	p.checkWorktree(opts)
	p.resolveTargets(ctx)
	p.resolveCommands()
	p.resolveFiles()
	p.resolveArtifacts()

	return p, nil
}

func (p *Plan) loadConfig(moduleDir string) {
	path := filepath.Join(moduleDir, ConfigFile)

	data, err := os.ReadFile(path)
	if err != nil {
		// Absence is the primary path, not a problem.
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.note("config", "none", "zero-config defaults")
		return
	}

	file, err := config.Parse(ConfigFile, data)
	if err != nil {
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.add("config", Fail, "%v", err)
		return
	}
	cfg, err := config.Decode(file)
	if err != nil {
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.add("config", Fail, "%v", err)
		return
	}

	p.Config, p.ConfigPath = cfg, path
	p.note("config", ConfigFile, "repository root")
}

func (p *Plan) resolveProject() {
	if p.Config.Project != "" {
		p.Project = p.Config.Project
		p.note("project", p.Project, ConfigFile)
		return
	}
	p.Project = p.Module.Name
	p.note("project", p.Project, "last element of the module path")
}

func (p *Plan) resolveVersion(ctx context.Context) {
	if p.Snapshot {
		base := "0.0.0"
		if prev, err := discover.PreviousTag(ctx, p.Module.Dir); err == nil && prev != "" {
			base = strings.TrimPrefix(prev, "v")
		}
		p.Version = fmt.Sprintf("%s-next+%s", base, p.Git.ShortCommit)
		p.note("version", p.Version, "snapshot of the previous tag")
		return
	}

	var versions []string
	for _, tag := range p.Git.Tags {
		if len(tag) > 1 && tag[0] == 'v' && tag[1] >= '0' && tag[1] <= '9' {
			versions = append(versions, tag)
		}
	}

	switch len(versions) {
	case 0:
		p.add("tag", Fail, "HEAD has no version tag; tag a release or use --snapshot")
		return
	case 1:
		// The ordinary case.
	default:
		p.add("tag", Fail, "HEAD carries several version tags (%s); it is ambiguous which is being released",
			strings.Join(versions, ", "))
		return
	}

	p.Tag = versions[0]
	p.Version = strings.TrimPrefix(p.Tag, "v")
	p.note("version", p.Version, "git tag "+p.Tag)
	p.add("tag", Pass, "%s is the only version tag on HEAD", p.Tag)

	// The check nothing else in this category performs.
	if err := p.Module.CheckTag(p.Tag); err != nil {
		p.add("module path", Fail, "%v", err)
	} else {
		p.add("module path", Pass, "%s agrees with tag %s", p.Module.Path, p.Tag)
	}
}

func (p *Plan) checkWorktree(opts Options) {
	switch {
	case p.Git.Clean:
		p.add("worktree", Pass, "clean")
	case opts.AllowDirty || opts.Snapshot:
		p.add("worktree", Warn, "uncommitted changes; artifacts will not be reproducible from this commit")
	default:
		p.add("worktree", Fail, "uncommitted changes; a release must be reproducible from its commit alone")
	}
}

func (p *Plan) resolveTargets(ctx context.Context) {
	if len(p.Config.Targets) > 0 {
		targets, err := gobuild.ParseTargets(p.Config.Targets)
		if err != nil {
			p.add("targets", Fail, "%v", err)
			return
		}
		p.Targets = targets
		p.note("targets", summarise(targets), ConfigFile)
	} else {
		p.Targets = gobuild.ReleaseTargets
		p.note("targets", summarise(p.Targets), "default matrix")
	}

	if err := gobuild.Validate(ctx, "", p.Targets); err != nil {
		p.add("targets", Fail, "%v", err)
		return
	}
	p.add("targets", Pass, "%d targets, all buildable by this toolchain", len(p.Targets))
}

// versionVars are the conventional linker-injected variables.
var versionVars = []string{"version", "commit", "date"}

func (p *Plan) resolveCommands() {
	commands, err := discover.FindMainPackages(p.Module.Dir, p.Project)
	if err != nil {
		p.add("commands", Fail, "%v", err)
		return
	}
	p.Commands = commands

	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.RelPath
	}
	p.note("commands", strings.Join(names, ", "), "./cmd/* or the module root")
	p.add("commands", Pass, "%d main package(s)", len(commands))

	p.resolveLDFlags()
}

// resolveLDFlags injects version metadata only into variables that can
// actually receive it.
//
// The linker accepts -X for a symbol that does not exist without complaint, so
// a wrong name produces a binary reporting its compiled-in default forever.
// Checking first turns that silent failure into a message at plan time, and
// injecting only what works means a project that does not declare these
// variables is not handed flags that do nothing.
func (p *Plan) resolveLDFlags() {
	p.LDFlags = append(p.LDFlags, p.Config.LDFlags...)

	var problems []string
	injected := 0

	for _, cmd := range p.Commands {
		names := make([]string, len(versionVars))
		for i, v := range versionVars {
			names[i] = v
		}

		symbols, err := discover.InspectVars(cmd.Dir, names)
		if err != nil {
			p.add("version injection", Warn, "%v", err)
			return
		}

		for _, sym := range symbols {
			switch sym.Status {
			case discover.SymbolOK:
				injected++
			case discover.SymbolMissing:
				// Not declaring these is a legitimate choice, so their absence
				// is silent. Declaring one wrongly is not.
			default:
				detail := fmt.Sprintf("main.%s in %s: %s", sym.Name, cmd.RelPath, sym.Detail)
				if sym.Suggestion != "" {
					detail += " (" + sym.Suggestion + ")"
				}
				problems = append(problems, detail)
			}
		}
	}

	switch {
	case len(problems) > 0:
		p.add("version injection", Fail, "%s", strings.Join(problems, "\n"))
	case injected == 0:
		p.add("version injection", Skip, "no main.version, main.commit or main.date declared")
	default:
		p.add("version injection", Pass, "%d symbol(s) verified before injection", injected)
	}
}

func (p *Plan) resolveFiles() {
	if len(p.Config.ArchiveFiles) > 0 {
		p.Files = p.Config.ArchiveFiles
		p.note("archive files", strings.Join(p.Files, ", "), ConfigFile)
		return
	}

	// Conventional documentation, included when present.
	for _, candidate := range []string{"README.md", "README", "LICENSE", "LICENSE.md", "CHANGELOG.md"} {
		if _, err := os.Stat(filepath.Join(p.Module.Dir, candidate)); err == nil {
			p.Files = append(p.Files, candidate)
		}
	}
	if len(p.Files) > 0 {
		p.note("archive files", strings.Join(p.Files, ", "), "found in the repository root")
	}
}

func (p *Plan) resolveArtifacts() {
	if p.Version == "" || len(p.Commands) == 0 {
		return
	}

	for _, cmd := range p.Commands {
		name := p.Project
		// With several commands the project name cannot identify an artifact.
		if len(p.Commands) > 1 {
			name = cmd.BinaryName
		}
		for _, target := range p.Targets {
			format := archive.FormatTarGz
			if target.OS == "windows" {
				format = archive.FormatZip
			}
			p.Artifacts = append(p.Artifacts, Artifact{
				Name:    fmt.Sprintf("%s_%s_%s_%s%s", name, p.Version, target.OS, target.Arch, format.Ext()),
				Target:  target,
				Command: cmd,
				Format:  format,
			})
		}
	}
}

func summarise(targets []gobuild.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.String()
	}
	return strings.Join(names, ", ")
}
