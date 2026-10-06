package forgerelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/plan"
)

// Observe says what Run would do to the release and its assets, without doing
// it: it reads what the forge holds and compares, by the same rule Run uses.
func Observe(ctx context.Context, o Options) ([]plan.Action, error) {
	existing, err := o.Client.ReleaseByTag(ctx, o.Repo, o.Release.TagName)
	if err != nil {
		return nil, err
	}

	release := plan.Action{Kind: plan.KindRelease, Target: o.Release.TagName, Op: plan.Add}
	if existing == nil {
		actions := []plan.Action{release}
		for _, name := range o.Files {
			actions = append(actions, plan.Action{
				Op: plan.Add, Kind: plan.KindAsset, Target: name, Planned: digestOf(o.Sums[name]),
			})
		}
		return actions, nil
	}

	release.Op = plan.Keep
	if body, _ := notesFor(existing, o); body != existing.Body && strings.TrimSpace(body) != "" {
		release.Op = plan.Change
	}
	actions := []plan.Action{release}

	assets, err := o.Client.Assets(ctx, o.Repo, existing.ID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]github.Asset, len(assets))
	for _, a := range assets {
		byName[a.Name] = a
	}

	for _, name := range o.Files {
		info, err := os.Stat(filepath.Join(o.Dir, name))
		if err != nil {
			return nil, fmt.Errorf("publish: %w", err)
		}
		action := plan.Action{Kind: plan.KindAsset, Target: name, Planned: digestOf(o.Sums[name])}
		remote, found := byName[name]
		switch {
		case !found:
			action.Op = plan.Add
		case matches(remote, o.Sums[name], info.Size()) == match:
			action.Op, action.Observed = plan.Keep, action.Planned
		default:
			action.Op, action.Observed = plan.Change, remote.Digest
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func digestOf(sum string) string {
	if sum == "" {
		return ""
	}
	return "sha256:" + sum
}
