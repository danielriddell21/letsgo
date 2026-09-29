package doctor

import (
	"context"
	"os/exec"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// checkConfig reports whether letsgo.mod parsed and decoded cleanly (DR-4).
// cfgErr is nil both when the file is absent and when it parsed fine —
// either way the repository's config is usable.
func (r *Result) checkConfig(cfgErr error) {
	if cfgErr != nil {
		r.add(repositoryGroup, "letsgo.mod", Fail, "", "%v", cfgErr)
		return
	}
	r.add(repositoryGroup, "letsgo.mod", OK, "", "parses")
}

// checkPlugins reports one check per pinned plugin (DR-5).
func (r *Result) checkPlugins(cfg *config.Config) {
	for _, p := range cfg.Plugins {
		r.checkPlugin(p)
	}
}

// checkPlugin reports whether p's pin is installed and its digest matches,
// mirroring cmd/letsgo/plugin.go's pluginStatus: the store is checked first,
// falling back to a PATH lookup plus plugin.DigestOf — the same algorithm
// plugin.Run itself uses to resolve a pin (DR-11), reimplemented here since
// it lives in another, unexported function.
func (r *Result) checkPlugin(p config.Plugin) {
	if store, err := pluginstore.Open(""); err == nil {
		if path, ok, err := store.Lookup(p.Digest, p.Command); err != nil {
			r.add(repositoryGroup, "plugin", Fail, "", "%s %s: %v", p.Command, p.Version, err)
			return
		} else if ok {
			r.add(repositoryGroup, "plugin", OK, "", "%s %s: %s", p.Command, p.Version, path)
			return
		}
	}

	path, err := exec.LookPath(p.Command)
	if err != nil {
		r.add(repositoryGroup, "plugin", Fail, "", "%s %s: not installed", p.Command, p.Version)
		return
	}

	digest, err := plugin.DigestOf(path)
	if err != nil {
		r.add(repositoryGroup, "plugin", Fail, "", "%s %s: %v", p.Command, p.Version, err)
		return
	}
	if digest != p.Digest {
		r.add(repositoryGroup, "plugin", Fail, "", "%s %s: digest mismatch (installed %s)",
			p.Command, p.Version, shortDigest(digest))
		return
	}
	r.add(repositoryGroup, "plugin", OK, "", "%s %s: %s", p.Command, p.Version, path)
}

// shortDigest shortens a "sha256:…" digest for a one-line report.
func shortDigest(digest string) string {
	trimmed, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(trimmed) < 12 {
		return digest
	}
	return "sha256:" + trimmed[:12] + "…"
}

// checkHistory reports a shallow clone or a scope with no tags yet (DR-6) —
// either one means the changelog and version-bump logic have less history to
// work with than they would on a full clone.
func (r *Result) checkHistory(ctx context.Context, dir string, git discover.Git, scope discover.Scope) {
	const hint = "use fetch-depth: 0"

	if git.Shallow {
		r.add(repositoryGroup, "history", Warn, hint, "shallow clone")
		return
	}

	tags, err := discover.Tags(ctx, dir, scope.Prefix)
	if err != nil {
		r.add(repositoryGroup, "history", Fail, "", "%v", err)
		return
	}
	if len(tags) == 0 {
		r.add(repositoryGroup, "history", Warn, hint, "no tags")
		return
	}
	r.add(repositoryGroup, "history", OK, "", "%d tag(s)", len(tags))
}

// checkRemote reports a non-GitHub origin (DR-7): the changelog and release
// publish steps only know how to talk to GitHub, so a release from this
// clone would skip them. A missing or unparseable origin is not a problem
// doctor reports — plan.Resolve treats it the same way, best-effort.
func (r *Result) checkRemote(ctx context.Context, dir string) {
	repo, err := discover.FindRepo(ctx, dir)
	if err != nil {
		return
	}
	if repo.Host != "github.com" {
		r.add(repositoryGroup, "remote", Warn, "", "%s is not github.com; changelog and publish steps will be skipped", repo.Host)
		return
	}
	r.add(repositoryGroup, "remote", OK, "", "%s", repo.String())
}

// checkWorktree reports uncommitted changes (DR-8): a release must be
// reproducible from its commit alone.
func (r *Result) checkWorktree(git discover.Git) {
	if !git.Clean {
		r.add(repositoryGroup, "worktree", Warn, "", "uncommitted changes")
		return
	}
	r.add(repositoryGroup, "worktree", OK, "", "clean")
}
