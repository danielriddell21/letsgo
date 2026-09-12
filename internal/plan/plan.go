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
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/publish/github"
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

	// Publish adds the checks a release needs but a local build does not:
	// a forge to publish to, and a token permitted to write there.
	Publish bool

	// Token overrides the token read from the environment.
	Token string

	// APIEndpoint overrides the forge API host. Empty means the real one.
	APIEndpoint string
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

	if opts.Publish {
		p.checkForge(ctx, opts)
	}

	return p, nil
}

// TokenEnvVars are the environment variables consulted for a forge token, in
// order. GH_TOKEN is what the GitHub CLI sets, so a machine already set up to
// use gh needs no further configuration.
var TokenEnvVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// Token returns the resolved token, and where it came from.
func Token(override string) (token, source string) {
	if override != "" {
		return override, "--token"
	}
	for _, name := range TokenEnvVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return "", ""
}

func underActions() bool { return os.Getenv("GITHUB_ACTIONS") == "true" }

// unconfirmedUnderActions says what could not be established and where to
// look if the publish then fails, without asserting a permission is missing.
func unconfirmedUnderActions(repo string) string {
	return fmt.Sprintf(
		"write access to %s could not be confirmed\n"+
			"a workflow token's permissions are not described by the repository\n"+
			"endpoint, so this is not evidence that it lacks them\n"+
			"if publishing fails: check `permissions: contents: write` in the\n"+
			"workflow, and Settings \u2192 Actions \u2192 General \u2192 Workflow permissions",
		repo)
}

// noActionsWriteAccess explains a refusal in terms of where the permission is
// granted to a workflow, which is not where a token's scopes live.
//
// A workflow may request no more than the repository allows, so
// `permissions: contents: write` has no effect while the repository default is
// read-only — and that setting is several screens away from the workflow file
// the reader is looking at.
func noActionsWriteAccess(repo string) string {
	return fmt.Sprintf(
		"this workflow's token may not create releases in %s\n"+
			"check both: `permissions: contents: write` in the workflow, and\n"+
			"Settings \u2192 Actions \u2192 General \u2192 Workflow permissions\n"+
			"\u2192 \"Read and write permissions\"",
		repo)
}

// noWriteAccess explains a missing permission in terms of where it is granted.
// Only reached outside Actions, where the reported permission is trustworthy.
func noWriteAccess(source, repo string) string {
	return fmt.Sprintf(
		"%s cannot write to %s; a release needs contents:write\n"+
			"a fine-grained token needs the Contents repository permission set to\n"+
			"Read and write; a classic token needs the repo scope",
		source, repo)
}

// checkForge verifies, before anything is built, that there is somewhere to
// publish and permission to do it. One API call now is worth more than a
// perfect set of artifacts and a 401.
func (p *Plan) checkForge(ctx context.Context, opts Options) {
	if !p.HasRepo {
		p.add("forge", Fail, "no 'origin' remote, so there is nowhere to publish")
		return
	}
	if p.Repo.Host != "github.com" {
		p.add("forge", Fail, "%s is not supported yet; letsgo publishes to github.com", p.Repo.Host)
		return
	}

	token, source := Token(opts.Token)
	if token == "" {
		p.add("token", Fail, "no token; set %s", strings.Join(TokenEnvVars, " or "))
		return
	}

	client := github.New(token)
	if opts.APIEndpoint != "" {
		client.SetEndpoints(opts.APIEndpoint, opts.APIEndpoint)
	}
	access, err := client.CheckAccess(ctx, github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name})
	if err != nil {
		// Unreachable is definitive: the repository is private to this token,
		// renamed, or gone.
		p.add("token", Fail, "%v", err)
		return
	}
	if access.Archived {
		p.add("token", Fail, "%s is archived and cannot receive a release", p.Repo)
		return
	}

	switch {
	case access.CanPush:
		p.add("token", Pass, "%s can write to %s", source, p.Repo)

	case !underActions():
		// For a user token the reported permission is accurate, so this is a
		// real answer and worth stopping for.
		p.add("token", Fail, "%s", noWriteAccess(source, p.Repo.String()))
		return

	default:
		// A workflow token is an installation token, whose permissions the
		// repository endpoint does not describe. Ask the forge directly.
		allowed, err := client.CanCreateRelease(ctx, github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name})
		switch {
		case err != nil:
			// Neither established nor refuted. Blocking here would refuse
			// correctly configured releases on no evidence, and the publish
			// attempt will give a definitive answer shortly.
			p.add("token", Warn, "%s", unconfirmedUnderActions(p.Repo.String()))
		case allowed:
			p.add("token", Pass, "%s may create releases in %s", source, p.Repo)
		default:
			p.add("token", Fail, "%s", noActionsWriteAccess(p.Repo.String()))
			return
		}
	}

	p.note("token", source, "environment")
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

	// Conventional documentation, included when present. The list lives in
	// internal/build so verification reaches the same answer from the same
	// tree rather than keeping a second copy of it.
	p.Files = build.FindDocumentation(p.Module.Dir)

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
