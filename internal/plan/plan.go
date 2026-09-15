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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/semver"
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

// ImageTarget is where a release's container images go.
type ImageTarget struct {
	// Registry is the host as it is written and published — "docker.io", not
	// the "registry-1.docker.io" its API answers on. APIHost is the latter.
	// Recording the written form matters: it is what goes in the manifest and
	// what someone types into `docker pull`.
	Registry string
	APIHost  string

	// Repository is the path under the registry. A module with one command
	// publishes to Repository; one with several publishes each command to
	// Repository/<binary>, because a repository holds one image.
	Repository string

	// Base is the image to stack on. The zero value means scratch.
	Base oci.Reference

	// Cmd is the default argument list, and Expose the ports to record. Both
	// are config verbatim: they are strings in the image config, so the commit
	// pins them exactly as it pins the reference.
	Cmd    []string
	Expose []string

	// Platforms are the targets that get an image, which is the Linux subset
	// of the build matrix: nothing else runs in a container.
	Platforms []gobuild.Target
}

// Repos returns the repository each binary publishes to.
func (t *ImageTarget) Repos(binaries []string) map[string]string {
	out := make(map[string]string, len(binaries))
	for _, binary := range binaries {
		if len(binaries) == 1 {
			out[binary] = t.Repository
			continue
		}
		out[binary] = t.Repository + "/" + binary
	}
	return out
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
	p.resolveBudgets()
	p.resolveCommands()
	p.resolveFiles()
	p.resolveArtifacts()

	p.resolveTap()
	p.resolveImage(ctx)

	if opts.Publish {
		p.checkForge(ctx, opts)
	}
	if opts.Analyse {
		p.checkVulnerabilities(ctx, opts)
		p.checkAPICompatibility(ctx, opts)
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

// checkVulnerabilities refuses to publish a binary that can reach known
// vulnerable code.
//
// The claim is deliberately narrow. Not "this release has no vulnerable
// dependencies", which is unachievable and would block every release, but
// "nothing in this binary can execute code with a known advisory against it".
// That is checkable, actionable, and rare enough to be worth stopping for.
func (p *Plan) checkVulnerabilities(ctx context.Context, opts Options) {
	found, err := gate.Vulncheck(ctx, p.Module.Dir)

	switch {
	case errors.Is(err, gate.ErrToolMissing):
		// A gate that did not run is not a gate that passed.
		p.add("vulnerabilities", Skip, "%v", err)
		return
	case err != nil:
		p.add("vulnerabilities", Warn, "could not be checked: %v", err)
		return
	case len(found) == 0:
		p.add("vulnerabilities", Pass, "no reachable vulnerabilities")
		return
	}

	lines := make([]string, 0, len(found)+1)
	for _, v := range found {
		lines = append(lines, v.String())
	}

	if opts.AllowVulnerable {
		// Recorded rather than suppressed: the manifest carries gate results,
		// so a consumer can see what this release was published in spite of.
		p.add("vulnerabilities", Warn, "%s\naccepted with --allow-vulnerable", strings.Join(lines, "\n"))
		return
	}

	lines = append(lines, "override with --allow-vulnerable")
	p.add("vulnerabilities", Fail, "%s", strings.Join(lines, "\n"))
}

// checkAPICompatibility refuses a release whose version promises more
// compatibility than its API delivers.
//
// Within a major version, removing or changing an exported symbol breaks every
// dependant at compile time. Go's answer is a new major version with a new
// import path, and nothing enforces it, so the mistake is made quietly and
// found by other people.
func (p *Plan) checkAPICompatibility(ctx context.Context, opts Options) {
	if p.Tag == "" {
		p.add("api compatibility", Skip, "not a tagged release")
		return
	}

	previous, err := discover.PreviousTag(ctx, p.Module.Dir)
	if err != nil || previous == "" {
		p.add("api compatibility", Skip, "no earlier release to compare against")
		return
	}

	old, cleanup, err := checkoutTag(ctx, p.Module.Dir, previous)
	if err != nil {
		p.add("api compatibility", Warn, "could not check out %s: %v", previous, err)
		return
	}
	defer cleanup()

	changes, err := gate.APIDiff(ctx, old, p.Module.Dir)
	switch {
	case errors.Is(err, gate.ErrToolMissing):
		p.add("api compatibility", Skip, "%v", err)
		return
	case err != nil:
		p.add("api compatibility", Warn, "could not be checked: %v", err)
		return
	}
	p.APIChanges = changes

	breaking := gate.Incompatibles(changes)
	if len(breaking) == 0 {
		p.add("api compatibility", Pass, "the exported API is backward compatible with %s", previous)
		return
	}

	// A major bump is exactly what an incompatible change calls for, so
	// making one is the correct outcome rather than a problem.
	if bumpBetween(previous, p.Tag) == "major" {
		p.add("api compatibility", Pass, "%d incompatible change(s), and %s is a major release",
			len(breaking), p.Tag)
		return
	}

	if opts.AllowBreaking {
		p.add("api compatibility", Warn, "%s\naccepted with --allow-breaking", describe(breaking))
		return
	}

	p.add("api compatibility", Fail,
		"%s is not a major release, but the API is not backward compatible with %s\n%s\n%s",
		p.Tag, previous, describe(breaking),
		"a breaking change needs a major version and a matching /vN module path\noverride with --allow-breaking")
}

func describe(changes []gate.Change) string {
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, "  "+c.String())
	}
	return strings.Join(lines, "\n")
}

// bumpBetween reports how two versions differ.
func bumpBetween(previous, current string) string {
	from, okFrom := semver.Parse(previous)
	to, okTo := semver.Parse(current)
	if !okFrom || !okTo {
		return "unknown"
	}
	switch {
	case to.Major != from.Major:
		return "major"
	case to.Minor != from.Minor:
		return "minor"
	default:
		return "patch"
	}
}

// checkoutTag puts a tag's tree somewhere it can be compared against.
func checkoutTag(ctx context.Context, repoDir, tag string) (dir string, cleanup func(), err error) {
	base, err := os.MkdirTemp("", "letsgo-apidiff-")
	if err != nil {
		return "", nil, fmt.Errorf("plan: scratch directory: %w", err)
	}

	dir = filepath.Join(base, "old")
	if err := discover.AddWorktree(ctx, repoDir, dir, tag); err != nil {
		_ = os.RemoveAll(base)
		return "", nil, err
	}

	return dir, func() {
		_ = discover.RemoveWorktree(ctx, repoDir, dir)
		_ = os.RemoveAll(base)
	}, nil
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

	p.checkTap(ctx, client)
}

// resolveTap parses the configured Homebrew tap.
//
// Separate from checkTap so that a malformed tap is reported by a plain
// `letsgo plan`, which contacts nothing.
func (p *Plan) resolveTap() {
	if p.Config.BrewTap == "" {
		return
	}
	tap, err := brew.ParseTap(p.Config.BrewTap)
	if err != nil {
		p.add("brew tap", Fail, "%v", err)
		return
	}
	p.Tap = tap
	p.note("brew tap", tap.String(), ConfigFile)
}

// resolveImage works out where the container images go.
//
// The reference names a repository and nothing else: the tag comes from the
// release, so accepting one here would create two answers to what a release is
// called and let them disagree.
func (p *Plan) resolveImage(ctx context.Context) {
	if p.Config.Image == nil {
		return
	}

	reference, source := p.Config.Image.Reference, ConfigFile
	if reference == "" {
		if !p.HasRepo {
			p.add("image", Fail,
				"no 'origin' remote, so there is no default image name; write one after `image`")
			return
		}
		// ghcr.io mirrors the repository it is released from, which is the
		// one name nobody has to be told.
		reference = "ghcr.io/" + p.Repo.Owner + "/" + p.Project
		source = "the repository owner and project name"
	}

	ref, err := oci.ParseReference(reference)
	if err != nil {
		p.add("image", Fail, "%v", err)
		return
	}
	if ref.Tag != "" || ref.Digest != "" {
		p.add("image", Fail,
			"%s carries a tag; the tag comes from the release, so name the repository only", reference)
		return
	}

	target := &ImageTarget{
		Registry: ref.Registry, APIHost: ref.APIHost(), Repository: ref.Repository,
		Cmd: p.Config.Image.Cmd, Expose: p.Config.Image.Expose,
	}
	for _, t := range p.Targets {
		if t.OS == "linux" {
			target.Platforms = append(target.Platforms, t)
		}
	}
	if len(target.Platforms) == 0 {
		p.add("image", Fail, "an image was asked for but no linux target is built")
		return
	}

	if base := p.Config.Image.Base; base != "" {
		parsed, err := oci.ParseReference(base)
		if err != nil {
			p.add("image", Fail, "image base: %v", err)
			return
		}
		target.Base = parsed
	}

	p.Image = target
	p.note("image", ref.Registry+"/"+ref.Repository, source)

	if target.Base == (oci.Reference{}) {
		p.add("image", Pass, "%s on scratch, for %d platform(s)", reference, len(target.Platforms))
		return
	}
	p.checkBase(ctx, reference, target)
}

// checkBase resolves the base image while planning rather than while building.
//
// Whether a base can be fetched is a fact about the release, and plan's whole
// purpose is to surface those in two seconds rather than after a cross-compile
// of every target. Resolving it here also turns the "named by tag" warning
// from advice into an instruction: it can name the digest to pin.
//
// One platform is enough to answer the question. The release resolves each
// one and caches them, which is a different job.
func (p *Plan) checkBase(ctx context.Context, reference string, target *ImageTarget) {
	registry := oci.NewRegistry(target.Base.APIHost())
	registry.UserAgent = "letsgo"

	platform := oci.Platform{OS: "linux", Architecture: target.Platforms[0].Arch}

	base, err := oci.ResolveBase(ctx, registry, target.Base, platform)

	status, detail := baseResult(reference, target, base, err)
	p.add("image", status, "%s", detail)
}

// baseResult judges a base resolution.
//
// Separated from the fetch above because the judgement is the part worth
// testing: whether a failure is the config's fault turns on who answered, and
// that distinction should not need a registry to exercise.
func baseResult(reference string, target *ImageTarget, base *oci.Base, err error) (Status, string) {
	where := fmt.Sprintf("%s on %s, for %d platform(s)", reference, target.Base, len(target.Platforms))

	if err != nil {
		// A registry that answered is reporting a real problem with the
		// reference — the wrong repository, a tag that does not exist, a
		// private image. One that could not be reached says nothing about the
		// config, and planning offline is worth keeping.
		var answered *oci.Error
		if errors.As(err, &answered) {
			return Fail, err.Error()
		}
		return Warn, fmt.Sprintf(
			"%s\n  the base could not be resolved, so it was not checked: %v", where, err)
	}

	if target.Base.Digest == "" {
		// A tag is a moving target. The resolved digest is recorded in the
		// manifest either way, so this is a warning rather than a refusal —
		// and naming the digest makes the fix a copy and paste.
		return Warn, fmt.Sprintf(
			"%s\n  the base is named by tag, so two releases of the same commit can differ; "+
				"pin it with @%s", where, base.IndexDigest)
	}
	return Pass, where
}

// checkTap establishes that the formula has somewhere to go before anything is
// built. A release that succeeds and then cannot update the tap has left the
// two out of step, which is worse than not starting.
func (p *Plan) checkTap(ctx context.Context, client *github.Client) {
	if p.Tap == (github.Repo{}) {
		return
	}

	access, err := client.CheckAccess(ctx, p.Tap)
	switch {
	case err != nil:
		p.add("brew tap", Fail, "%v", err)
	case access.Archived:
		p.add("brew tap", Fail, "%s is archived and cannot receive a formula", p.Tap)
	case access.CanPush:
		p.add("brew tap", Pass, "%s can receive the formula", p.Tap)
	case underActions():
		// Same limitation as the release token: an installation token's
		// permissions are not described by the repository endpoint, and a
		// tap in another repository needs a token this one cannot inspect.
		p.add("brew tap", Warn,
			"whether this token can write to %s cannot be confirmed from inside Actions\n"+
				"  a workflow token cannot write to another repository; the tap needs a PAT or an App token",
			p.Tap)
	default:
		p.add("brew tap", Fail, "this token cannot write to %s", p.Tap)
	}
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

// resolveBudgets parses the configured size caps.
//
// A budget naming a target that is not built would silently never apply,
// which is the failure mode a size budget exists to prevent, so it is a
// mistake rather than a no-op.
func (p *Plan) resolveBudgets() {
	if len(p.Config.Budgets) == 0 {
		return
	}

	built := make(map[string]bool, len(p.Targets))
	for _, t := range p.Targets {
		built[t.String()] = true
	}

	budgets := make(map[string]bytesize.Size, len(p.Config.Budgets))
	var problems []string
	for _, target := range sortedKeys(p.Config.Budgets) {
		size, err := bytesize.Parse(p.Config.Budgets[target])
		if err != nil {
			problems = append(problems, fmt.Sprintf("budget %s: %v", target, err))
			continue
		}
		if !built[target] {
			problems = append(problems,
				fmt.Sprintf("budget names %s, which is not a target this release builds", target))
			continue
		}
		budgets[target] = size
	}

	if len(problems) > 0 {
		p.add("budgets", Fail, "%s", strings.Join(problems, "\n"))
		return
	}
	p.Budgets = budgets

	described := make([]string, 0, len(budgets))
	for _, target := range sortedKeys(budgets) {
		described = append(described, target+" "+budgets[target].String())
	}
	p.note("budgets", strings.Join(described, ", "), ConfigFile)
	p.add("budgets", Pass, "%d target(s) capped; checked once the binaries exist", len(budgets))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
		copy(names, versionVars)

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
