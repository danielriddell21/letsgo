package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/danielriddell21/letsgo/internal/releases"
)

// source reads releases through the package's own HTTP, so that selfupdate
// shares the reader's selection rules without pulling the publishing
// toolchain's forge client into every program that self-updates.
type source struct{ o Options }

var (
	_ releases.TagSource  = (*source)(nil)
	_ releases.ListSource = (*source)(nil)
	_ releases.Downloader = (*source)(nil)
)

// wireRelease is the forge's JSON for a release.
type wireRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (w wireRelease) published() releases.Published {
	assets := make([]releases.Asset, len(w.Assets))
	for i, a := range w.Assets {
		assets[i] = releases.Asset{Name: a.Name, URL: a.BrowserDownloadURL}
	}
	return releases.Published{
		Tag:        w.TagName,
		Body:       w.Body,
		URL:        w.HTMLURL,
		Draft:      w.Draft,
		Prerelease: w.Prerelease,
		Assets:     assets,
	}
}

// fetchJSON GETs path under the repository and decodes the body into out.
func (s *source) fetchJSON(ctx context.Context, path, what string, out any) error {
	resp, err := s.o.get(ctx, fmt.Sprintf("%s/repos/%s/%s", s.o.api(), s.o.Repo, path))
	if err != nil {
		return err
	}
	data, err := read(resp)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("selfupdate: parsing %s: %w", what, err)
	}
	return nil
}

func (s *source) release(ctx context.Context, path, what string) (*releases.Published, error) {
	var w wireRelease
	if err := s.fetchJSON(ctx, path, what, &w); err != nil {
		return nil, err
	}
	if w.TagName == "" {
		return nil, nil
	}
	p := w.published()
	return &p, nil
}

func (s *source) ReleaseByTag(ctx context.Context, tag string) (*releases.Published, error) {
	return s.release(ctx, "releases/tags/"+url.PathEscape(tag), "release "+tag)
}

func (s *source) LatestRelease(ctx context.Context) (*releases.Published, error) {
	return s.release(ctx, "releases/latest", "the latest release")
}

// ListReleases reads up to 100 releases, each already carrying what selection
// needs (tag, body, draft) and, for the winner, its assets.
func (s *source) ListReleases(ctx context.Context) ([]releases.Published, error) {
	var all []wireRelease
	if err := s.fetchJSON(ctx, "releases?per_page=100", "releases", &all); err != nil {
		return nil, err
	}
	out := make([]releases.Published, len(all))
	for i, w := range all {
		out[i] = w.published()
	}
	return out, nil
}

func (s *source) DownloadAsset(ctx context.Context, a releases.Asset) ([]byte, error) {
	resp, err := s.o.get(ctx, a.URL)
	if err != nil {
		return nil, err
	}
	return read(resp)
}
