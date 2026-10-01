package diff

import (
	"context"
	"fmt"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/releases/githubsource"
)

// Fetch loads the manifest a published release describes itself with.
//
// An empty tag means the most recent release, so that comparing against "what
// is out there now" does not require knowing what that is.
func Fetch(ctx context.Context, c *github.Client, repo github.Repo, scope discover.Scope, tag string) (*manifest.Manifest, error) {
	src := &githubsource.Source{Client: c, Repo: repo}

	release, err := find(ctx, src, repo, scope, tag)
	if err != nil {
		return nil, err
	}

	m, _, err := releases.Manifest(ctx, src, release)
	if releases.IsNoManifest(err) {
		return nil, fmt.Errorf(
			"diff: release %s has no %s, so there is nothing to compare\n"+
				"  only releases published by letsgo can be diffed",
			release.Tag, manifest.FileName)
	}
	return m, err
}

func find(ctx context.Context, src releases.LatestSource, repo github.Repo, scope discover.Scope, tag string) (*releases.Published, error) {
	if tag == "" {
		release, err := releases.Latest(ctx, src, scope)
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, fmt.Errorf("diff: %s has no releases", repo)
		}
		return release, nil
	}

	release, err := releases.ByTag(ctx, src, tag)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("diff: %s has no release tagged %s", repo, tag)
	}
	return release, nil
}
