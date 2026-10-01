// Package releases reads Published Releases back from the forge: it selects
// which one a command means, and fetches its Manifest.
//
// Before this package, verify, audit, diff, promote and selfupdate each
// picked "the" release and downloaded its manifest themselves, with different
// rules for scope, drafts, Yanks and prereleases (ADR-0024). The selection
// rules live here once; what varies is the Source a caller supplies.
//
// The package imports no forge client. Each Source is a small interface
// declared here, so a test passes an in-memory fake and production passes an
// adapter such as githubsource.
package releases

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// Published is a release as the forge holds it.
type Published struct {
	ID         int64
	Tag        string
	Name       string
	Body       string
	Draft      bool
	Prerelease bool
	Immutable  bool
	Assets     []Asset
}

// Asset is a file attached to a Published release.
type Asset struct {
	ID   int64
	Name string
	Size int64

	// SHA256 is the digest the forge reports, without an algorithm prefix,
	// or empty when it reports none.
	SHA256 string

	// URL is where the file can be downloaded without the forge's API, for
	// a Source that has no authenticated client.
	URL string
}

// Asset finds an attached file by name.
func (p *Published) Asset(name string) (Asset, bool) {
	for _, a := range p.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Retracted reports whether the release has been Yanked.
func (p *Published) Retracted() bool { return IsRetracted(p.Body) }

// IsRetracted reports whether a release body carries the notice `yank`
// prepends. Everything that reads releases must agree on what "retracted"
// means, so there is one test.
func IsRetracted(body string) bool {
	return strings.HasPrefix(body, "> [!CAUTION]")
}

// TagSource looks a release up by its tag. A missing release is nil with no
// error, and a draft is not found: the forge's own by-tag lookup hides them.
type TagSource interface {
	ReleaseByTag(ctx context.Context, tag string) (*Published, error)
}

// ListSource lists every release, drafts included.
type ListSource interface {
	ListReleases(ctx context.Context) ([]Published, error)
}

// LatestSource finds the latest release, unscoped or within one scope.
type LatestSource interface {
	TagSource

	// LatestRelease is the forge's own notion of latest: the most recent
	// release that is neither a draft nor a prerelease. Nil when none.
	LatestRelease(ctx context.Context) (*Published, error)

	// Tags lists tag names in the forge's order, which is not version order.
	Tags(ctx context.Context, limit int) ([]string, error)
}

// Downloader fetches an asset's content.
type Downloader interface {
	DownloadAsset(ctx context.Context, a Asset) ([]byte, error)
}

// scopedTagLimit is how many tags a scoped lookup reads. A module with more
// tags than this in a monorepo can miss its newest, as it could before.
const scopedTagLimit = 100

// ByTag finds a release by tag, hiding drafts. Nil when there is none.
func ByTag(ctx context.Context, src TagSource, tag string) (*Published, error) {
	return src.ReleaseByTag(ctx, tag)
}

// ByTagIncludingDrafts finds a release by tag, drafts included, which is the
// only way to tell a draft release candidate from one that was never made.
// Nil when there is none.
func ByTagIncludingDrafts(ctx context.Context, src ListSource, tag string) (*Published, error) {
	all, err := src.ListReleases(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Tag == tag {
			return &all[i], nil
		}
	}
	return nil, nil
}

// Latest finds the newest release.
//
// With an empty scope it is the forge's own latest, which skips drafts and
// prereleases. With a prefix the forge has no concept of the monorepo's
// scopes, so it lists tags and picks the highest version in the scope, and
// that can be a prerelease. Nil when there is none.
func Latest(ctx context.Context, src LatestSource, scope discover.Scope) (*Published, error) {
	if scope.Prefix == "" {
		return src.LatestRelease(ctx)
	}

	tags, err := src.Tags(ctx, scopedTagLimit)
	if err != nil {
		return nil, err
	}
	tag, ok := scope.LatestTag(tags)
	if !ok {
		return nil, nil
	}
	return src.ReleaseByTag(ctx, tag)
}

// PerMajor returns the newest stable release of each major version in scope,
// lowest major first. Drafts, retracted releases, prereleases and out-of-scope
// tags are skipped.
func PerMajor(ctx context.Context, src ListSource, scope discover.Scope) ([]*Published, error) {
	all, err := src.ListReleases(ctx)
	if err != nil {
		return nil, err
	}

	type candidate struct {
		release *Published
		version semver.Version
	}
	best := map[int]candidate{}
	for i := range all {
		r := &all[i]
		if r.Draft || r.Retracted() {
			continue
		}
		rest, ok := scope.MatchesTag(r.Tag)
		if !ok {
			continue
		}
		v, ok := semver.Parse(rest)
		if !ok || v.IsPrerelease() {
			continue
		}
		if cur, exists := best[v.Major]; !exists || semver.Compare(v, cur.version) > 0 {
			best[v.Major] = candidate{release: r, version: v}
		}
	}

	majors := make([]int, 0, len(best))
	for major := range best {
		majors = append(majors, major)
	}
	sort.Ints(majors)

	out := make([]*Published, 0, len(majors))
	for _, major := range majors {
		out = append(out, best[major].release)
	}
	return out, nil
}

// NoManifestError is returned when a release has no letsgo.json, so nothing
// describes what it should contain. Each caller words its own message around
// the tag.
type NoManifestError struct{ Tag string }

func (e *NoManifestError) Error() string {
	return fmt.Sprintf("release %s has no %s", e.Tag, manifest.FileName)
}

// Manifest downloads and decodes a release's letsgo.json. It also returns the
// sha256 of the bytes as published, which is what identifies the release.
func Manifest(ctx context.Context, d Downloader, p *Published) (*manifest.Manifest, []byte, error) {
	asset, ok := p.Asset(manifest.FileName)
	if !ok {
		return nil, nil, &NoManifestError{Tag: p.Tag}
	}
	data, err := d.DownloadAsset(ctx, asset)
	if err != nil {
		return nil, nil, err
	}
	m, err := manifest.Decode(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding %s's %s: %w", p.Tag, manifest.FileName, err)
	}
	sum := sha256.Sum256(data)
	return m, sum[:], nil
}

// IsNoManifest reports whether err is a NoManifestError.
func IsNoManifest(err error) bool {
	var e *NoManifestError
	return errors.As(err, &e)
}
