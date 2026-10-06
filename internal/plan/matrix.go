package plan

import (
	"context"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

func (p *Plan) resolveTargets(ctx context.Context) {
	if len(p.Config.Targets) > 0 {
		targets, err := gobuild.ParseTargets(p.Config.Targets)
		if err != nil {
			p.addAt(p.posOf("build"), "targets", Fail, "%v", err)
			return
		}
		p.Targets = targets
		p.note("targets", summarise(targets), config.FileName)
	} else {
		p.Targets = gobuild.ReleaseTargets
		p.note("targets", summarise(p.Targets), "default matrix")
	}

	if err := gobuild.Validate(ctx, "", p.Targets); err != nil {
		p.addAt(p.posOf("build"), "targets", Fail, "%v", err)
		return
	}
	p.add("targets", Pass, "%d targets, all buildable by this toolchain", len(p.Targets))

	p.resolveTags()
}

// resolveTags reads the build tags.
//
// Tags are recorded in the manifest and replayed by verification, so a tagged
// build is exactly as reproducible as an untagged one: the tag list is config,
// and config is pinned by the commit.
func (p *Plan) resolveTags() {
	if len(p.Config.Tags) == 0 {
		return
	}
	for _, tag := range p.Config.Tags {
		if !validTag(tag) {
			p.addAt(p.posOf("tags"), "tags", Fail, "%q is not a build tag", tag)
			return
		}
	}
	p.Tags = p.Config.Tags
	p.note("tags", strings.Join(p.Tags, ", "), config.FileName)
}

// validTag reports whether a build tag is one the toolchain will accept.
// Checked here because `-tags` takes a comma-separated list, so a tag
// containing a comma would silently become two.
func validTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '.':
		default:
			return false
		}
	}
	return true
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
	failed := false
	for _, target := range sortedKeys(p.Config.Budgets) {
		pos := p.configPos(p.Config.BudgetPos[target])
		size, err := bytesize.Parse(p.Config.Budgets[target])
		if err != nil {
			p.addAt(pos, "budgets", Fail, "budget %s: %v", target, err)
			failed = true
			continue
		}
		if !built[target] {
			p.addAt(pos, "budgets", Fail,
				"budget names %s, which is not a target this release builds", target)
			failed = true
			continue
		}
		budgets[target] = size
	}

	if failed {
		return
	}
	p.Budgets = budgets

	described := make([]string, 0, len(budgets))
	for _, target := range sortedKeys(budgets) {
		described = append(described, target+" "+budgets[target].String())
	}
	p.note("budgets", strings.Join(described, ", "), config.FileName)
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

func (p *Plan) resolveCommands(ctx context.Context) {
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

	p.resolveLDFlags(ctx)
}
