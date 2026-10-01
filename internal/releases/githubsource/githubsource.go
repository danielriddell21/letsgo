// Package githubsource adapts the forge client to the releases package's
// sources, for one repository.
package githubsource

import (
	"context"

	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/releases"
)

// Source reads one repository's releases through a github.Client.
type Source struct {
	Client *github.Client
	Repo   github.Repo
}

var (
	_ releases.LatestSource = (*Source)(nil)
	_ releases.ListSource   = (*Source)(nil)
	_ releases.Downloader   = (*Source)(nil)
)

func (s *Source) ReleaseByTag(ctx context.Context, tag string) (*releases.Published, error) {
	r, err := s.Client.ReleaseByTag(ctx, s.Repo, tag)
	return convertPtr(r), err
}

func (s *Source) LatestRelease(ctx context.Context) (*releases.Published, error) {
	r, err := s.Client.LatestRelease(ctx, s.Repo)
	return convertPtr(r), err
}

func (s *Source) ListReleases(ctx context.Context) ([]releases.Published, error) {
	all, err := s.Client.ListReleases(ctx, s.Repo)
	if err != nil {
		return nil, err
	}
	out := make([]releases.Published, len(all))
	for i := range all {
		out[i] = convert(&all[i])
	}
	return out, nil
}

func (s *Source) Tags(ctx context.Context, limit int) ([]string, error) {
	return s.Client.Tags(ctx, s.Repo, limit)
}

func (s *Source) DownloadAsset(ctx context.Context, a releases.Asset) ([]byte, error) {
	return s.Client.DownloadAsset(ctx, s.Repo, a.ID)
}

func convertPtr(r *github.Release) *releases.Published {
	if r == nil {
		return nil
	}
	p := convert(r)
	return &p
}

func convert(r *github.Release) releases.Published {
	assets := make([]releases.Asset, len(r.Assets))
	for i, a := range r.Assets {
		sum, _ := a.SHA256()
		assets[i] = releases.Asset{ID: a.ID, Name: a.Name, Size: a.Size, SHA256: sum}
	}
	return releases.Published{
		ID:         r.ID,
		Tag:        r.TagName,
		Name:       r.Name,
		Body:       r.Body,
		URL:        r.HTMLURL,
		Draft:      r.Draft,
		Prerelease: r.Prerelease,
		Immutable:  r.Immutable,
		Assets:     assets,
	}
}
