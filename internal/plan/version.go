package plan

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
)

// resolveModule settles which module is built.
//
// Without the directive that is the module beside letsgo.mod, which is what
// every single-module repository has. With it, a repository whose binary lives
// in a second module — a workspace where the renderer is split out so that
// importing the core does not pull in its dependencies — can release that
// module while still being one project with one version and one tag.
func (p *Plan) resolveModule() {
	if p.Config.ModuleDir == "" {
		p.note("module", p.Module.Path, "go.mod")
		return
	}

	dir := filepath.Join(p.RootDir, filepath.FromSlash(p.Config.ModuleDir))
	module, err := discover.FindModule(dir)
	if err != nil {
		p.addAt(p.posOf("module"), "module", Fail, "module %s: %v", p.Config.ModuleDir, err)
		return
	}

	// FindModule walks up, so a directory with no go.mod of its own resolves
	// to an ancestor's. That is a silent no-op rather than the nested module
	// that was asked for, and worth saying.
	if module.Dir != dir {
		p.addAt(p.posOf("module"), "module", Fail, "module %s: no go.mod in that directory", p.Config.ModuleDir)
		return
	}

	p.Module = module
	p.note("module", module.Path, config.FileName)
	p.add("module", Pass, "%s in %s", module.Path, p.Config.ModuleDir)

	// The tag names the repository's release, not a version of this nested
	// module — v1.2.0 is not a tag `go install module/web@v1.2.0` or the
	// module proxy will ever recognise. Scoped tags (a prefixed tag naming
	// this module directly) are what fixes this; until then, a warm that can
	// only fail is skipped rather than reported as a broken release.
	p.add("module proxy", Warn,
		"the tag has no %s prefix, so `go install %s@version` and the proxy warm cannot resolve it",
		p.Config.ModuleDir, module.Path)
}

// checkReplace fails a release whose go.mod replaces a dependency with a
// local filesystem path, rather than letting the failure surface later as an
// unrelated-looking build or reproducibility error.
func (p *Plan) checkReplace() {
	module, dir, err := discover.LocalReplace(filepath.Join(p.Module.Dir, "go.mod"))
	if err != nil {
		p.add("replace", Fail, "%v", err)
		return
	}
	if module != "" {
		p.add("replace", Fail,
			"go.mod replaces %s with the local path %q\n"+
				"  `go install` cannot resolve a local replacement, and the release's source archive does not contain it either",
			module, dir)
		return
	}
	p.add("replace", Pass, "no local-path replace directives")
}

func (p *Plan) resolveProject() {
	if p.Config.Project != "" {
		p.Project = p.Config.Project
		p.note("project", p.Project, config.FileName)
		return
	}
	// With a nested module the project is still the repository's: releasing
	// ./web does not make the project "web", and the archives, the formula and
	// the image would all be named after a directory.
	if p.Config.ModuleDir != "" && p.Location.Module.Name != "" {
		p.Project = p.Location.Module.Name
		p.note("project", p.Project, "last element of the repository's module path")
		return
	}

	p.Project = p.Module.Name
	p.note("project", p.Project, "last element of the module path")
}

func (p *Plan) resolveVersion(ctx context.Context, opts Options) {
	if p.Snapshot {
		base := "0.0.0"
		if prev, err := p.Runner.PreviousTag(ctx, p.Scope.Prefix); err == nil && prev != "" {
			base = strings.TrimPrefix(strings.TrimPrefix(prev, p.Scope.Prefix), "v")
		}
		p.Version = fmt.Sprintf("%s-next+%s", base, p.Git.ShortCommit)
		p.note("version", p.Version, "snapshot of the previous tag")
		return
	}

	prefix := p.Scope.Prefix
	versions := scopedVersionTags(p.Git.Tags, prefix)

	// opts.Tag pins which of several qualifying tags on HEAD is being
	// released. Ordinarily HEAD carries at most one, but `letsgo promote`
	// tags an RC's own commit with its stable version, and that commit
	// already carries the RC's own tag — so for that one caller, and only
	// when it says so explicitly, more than one tag on HEAD is not an error.
	if opts.Tag != "" {
		if !slices.Contains(versions, opts.Tag) {
			p.add("tag", Fail, "%s does not carry the requested tag %s", p.Git.Commit, opts.Tag)
			return
		}
		versions = []string{opts.Tag}
	}

	switch len(versions) {
	case 0:
		p.failNoVersionTag(prefix)
		return
	case 1:
		// The ordinary case.
	default:
		p.add("tag", Fail, "HEAD carries several version tags (%s); it is ambiguous which is being released",
			strings.Join(versions, ", "))
		return
	}

	p.Tag = versions[0]
	versionTag := strings.TrimPrefix(p.Tag, prefix)
	p.Version = strings.TrimPrefix(versionTag, "v")
	p.note("version", p.Version, "git tag "+p.Tag)
	p.add("tag", Pass, "%s is the only version tag on HEAD", p.Tag)

	// The check nothing else in this category performs.
	if err := p.Module.CheckTag(versionTag); err != nil {
		p.add("module path", Fail, "%v", err)
	} else {
		p.add("module path", Pass, "%s agrees with tag %s", p.Module.Path, p.Tag)
	}
}

// scopedVersionTags returns the tags that name a version of prefix's own
// scope, in the same order they were given.
func scopedVersionTags(tags []string, prefix string) []string {
	scope := discover.Scope{Prefix: prefix}
	var versions []string
	for _, tag := range tags {
		if _, ok := scope.MatchesTag(tag); ok {
			versions = append(versions, tag)
		}
	}
	return versions
}

// failNoVersionTag records that this scope has no version tag on HEAD,
// naming its own prefix when it has one: a root module's tag is a different
// scope entirely, not this one's missing tag.
func (p *Plan) failNoVersionTag(prefix string) {
	if prefix == "" {
		p.add("tag", Fail, "HEAD has no version tag; tag a release or use --snapshot")
		return
	}
	p.add("tag", Fail, "HEAD has no %sv version tag; tag a release or use --snapshot", prefix)
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
