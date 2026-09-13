package diff

import (
	"context"
	"fmt"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// Fetch loads the manifest a published release describes itself with.
//
// An empty tag means the most recent release, so that comparing against "what
// is out there now" does not require knowing what that is.
func Fetch(ctx context.Context, c *github.Client, repo github.Repo, tag string) (*manifest.Manifest, error) {
	release, err := find(ctx, c, repo, tag)
	if err != nil {
		return nil, err
	}

	asset, ok := release.Asset(manifest.FileName)
	if !ok {
		return nil, fmt.Errorf(
			"diff: release %s has no %s, so there is nothing to compare\n"+
				"  only releases published by letsgo can be diffed",
			release.TagName, manifest.FileName)
	}

	data, err := c.DownloadAsset(ctx, repo, asset.ID)
	if err != nil {
		return nil, err
	}
	return manifest.Decode(data)
}

func find(ctx context.Context, c *github.Client, repo github.Repo, tag string) (*github.Release, error) {
	if tag == "" {
		release, err := c.LatestRelease(ctx, repo)
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, fmt.Errorf("diff: %s has no releases", repo)
		}
		return release, nil
	}

	release, err := c.ReleaseByTag(ctx, repo, tag)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("diff: %s has no release tagged %s", repo, tag)
	}
	return release, nil
}
