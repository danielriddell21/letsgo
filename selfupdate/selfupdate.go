// Package selfupdate updates a binary released by letsgo, from inside that
// binary.
//
// Every release publishes a manifest recording each archive's digest and the
// digest of the binary inside it, so an updater does not have to trust what it
// downloads: it checks the archive against the manifest, and the extracted
// binary against the manifest again. Both digests were fixed by the build, and
// `letsgo verify` proves independently that the build came from the source.
// Self-update is the one place where a program replaces its own code, and it
// is worth being the place where the checking is strictest.
//
//	update, err := selfupdate.Check(ctx, selfupdate.Options{
//		Repo:    "you/tool",
//		Current: version,
//	})
//	if err != nil || update == nil {
//		return err // nil update means the running binary is current
//	}
//	return update.Apply(ctx)
//
// Nothing here writes anything until Apply is called, so a program can offer
// the update rather than take it.
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/manifest"
)

// maxDownload bounds what will be read from a release. An archive of a Go
// binary is tens of megabytes; past this it is a broken or hostile endpoint,
// and reading it would be a way to exhaust memory rather than a way to update.
const maxDownload = 512 << 20

// Options configure an update check.
type Options struct {
	// Repo is the repository to check, as "owner/name". Required.
	Repo string

	// Current is the running binary's version, with or without a leading "v".
	// An unparseable version means every release looks newer, which is the
	// right answer for a development build.
	Current string

	// HTTP is the client used for the forge and the download. The zero value
	// means a client with a sensible timeout.
	HTTP *http.Client

	// Token authenticates to the forge, for a private repository or to lift
	// the unauthenticated rate limit. Optional.
	Token string

	// UserAgent identifies the program checking for updates.
	UserAgent string

	// APIEndpoint overrides the forge API host. Used by tests.
	APIEndpoint string

	// OS and Arch override the platform to update to. Empty means this one.
	OS, Arch string

	// Tag selects one exact release instead of the newest. When it is set the
	// version comparison is skipped, because asking for a tag is asking for
	// that tag — including an older one.
	Tag string

	// Binary names the executable to fetch, for a repository that releases
	// several from one module. Empty takes the only one built for the
	// platform, which is what a single-command repository publishes.
	Binary string

	// Prefix is the module's scope prefix (see discover.Scope), empty for a
	// root module. It is only consulted when Tag is empty: it keeps "the
	// most recent release" from picking another module's release in a
	// monorepo.
	Prefix string

	// Channel selects a release channel instead of the newest stable release.
	// "" means stable, today's behaviour. A prerelease identifier such as
	// "beta" or "rc" follows that channel; "next" follows any prerelease.
	// Whichever of the channel's own newest release and the newest stable
	// release is newer wins, excluding drafts and yanked releases. Only
	// consulted when Tag is empty.
	Channel string
}

// Update is a release newer than the running binary.
type Update struct {
	// Version is the new version, without a leading "v"; Tag is the release
	// tag as published.
	Version string
	Tag     string

	// Notes is the release description, for a program that wants to show what
	// changed before replacing itself.
	Notes string

	// URL is the release page.
	URL string

	// Archive is the file that will be downloaded, and SHA256 the digest the
	// release recorded for it. BinarySHA256 is the digest of the executable
	// inside — checked after extraction, so a correct archive containing the
	// wrong binary is caught too.
	Archive      string
	SHA256       string
	BinarySHA256 string

	// Binary is the executable's name inside the archive.
	Binary string

	downloadURL string
	options     Options
}

// Check asks whether a newer release exists.
//
// A nil Update and a nil error mean the running binary is current, which is
// the common case and deliberately not an error.
func Check(ctx context.Context, o Options) (*Update, error) {
	if o.Repo == "" {
		return nil, fmt.Errorf("selfupdate: no repository given")
	}
	if o.OS == "" {
		o.OS = runtime.GOOS
	}
	if o.Arch == "" {
		o.Arch = runtime.GOARCH
	}

	release, err := resolveRelease(ctx, o)
	if err != nil {
		return nil, err
	}
	if o.Tag == "" && !newer(o.Current, release.Tag, o.Prefix) {
		return nil, nil
	}

	m, err := fetchManifest(ctx, o, release)
	if err != nil {
		return nil, err
	}

	artifact, ok := artifactFor(m, o.OS, o.Arch, o.Binary)
	if !ok {
		if o.Binary != "" {
			return nil, fmt.Errorf("selfupdate: %s has no %s/%s build of %s",
				release.Tag, o.OS, o.Arch, o.Binary)
		}
		return nil, fmt.Errorf("selfupdate: %s has no %s/%s build", release.Tag, o.OS, o.Arch)
	}

	asset, ok := release.Asset(artifact.Name)
	if !ok {
		return nil, fmt.Errorf("selfupdate: %s describes %s but does not attach it",
			release.Tag, artifact.Name)
	}

	return &Update{
		Version:      m.Version,
		Tag:          release.Tag,
		Notes:        release.Body,
		URL:          release.URL,
		Archive:      artifact.Name,
		SHA256:       artifact.SHA256,
		BinarySHA256: artifact.BinarySHA256,
		Binary:       binaryName(artifact, m.Project, o.OS),
		downloadURL:  asset.URL,
		options:      o,
	}, nil
}

// newer reports whether tag is a later version than current, within prefix's
// scope (see discover.Scope). A scoped tag carries that prefix ahead of its
// "vX.Y.Z", which is not itself part of the version to compare.
//
// An unparseable current version — "dev", or a bare commit — is treated as
// older than everything, because a development build asking about updates has
// no claim to being ahead of a release.
func newer(current, tag, prefix string) bool {
	rest, ok := (discover.Scope{Prefix: prefix}).MatchesTag(tag)
	if !ok {
		return false
	}
	next, ok := semver.Parse(rest)
	if !ok {
		return false
	}
	now, ok := semver.Parse(strings.TrimPrefix(current, "v"))
	if !ok {
		return true
	}
	return semver.Compare(next, now) > 0
}

// artifactFor picks the build for a platform, and for one named executable
// when the release carries several — a repository shipping three commands
// publishes three archives per platform, and os/arch alone does not say which.
func artifactFor(m *manifest.Manifest, goos, arch, binary string) (manifest.Artifact, bool) {
	for _, a := range m.Artifacts {
		if a.OS != goos || a.Arch != arch {
			continue
		}
		if binary != "" && a.Binary != binary {
			continue
		}
		return a, true
	}
	return manifest.Artifact{}, false
}

// binaryName is the executable inside the archive. Releases published before
// the manifest recorded it carry the project's name, which for a module with
// one command is the same answer.
func binaryName(a manifest.Artifact, project, goos string) string {
	name := a.Binary
	if name == "" {
		name = project
	}
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func (o Options) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (o Options) api() string {
	if o.APIEndpoint != "" {
		return strings.TrimSuffix(o.APIEndpoint, "/")
	}
	return "https://api.github.com"
}

func (o Options) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: %w", err)
	}

	agent := o.UserAgent
	if agent == "" {
		agent = "letsgo-selfupdate"
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "application/vnd.github+json")
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}

	resp, err := o.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("selfupdate: %s: %s", url, resp.Status)
	}
	return resp, nil
}

// read drains a response, bounded.
func read(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: reading response: %w", err)
	}
	return data, nil
}

// resolveRelease fetches the release asked for: one exact tag, a channel, or
// the newest stable release.
func resolveRelease(ctx context.Context, o Options) (*releases.Published, error) {
	src := &source{o: o}
	scope := discover.Scope{Prefix: o.Prefix}

	if o.Tag != "" {
		r, err := releases.ByTag(ctx, src, o.Tag)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, fmt.Errorf("selfupdate: %s has no release %s", o.Repo, o.Tag)
		}
		return r, nil
	}

	if o.Channel != "" {
		r, err := releases.Best(ctx, src, scope, func(v semver.Version) bool { return channelMatch(v, o.Channel) })
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, fmt.Errorf("selfupdate: %s has no releases on the %s channel", o.Repo, o.Channel)
		}
		return r, nil
	}

	// The forge's own "latest" has no concept of a monorepo's scopes, so a
	// scoped module lists releases instead and applies the same rules.
	var r *releases.Published
	var err error
	if o.Prefix == "" {
		r, err = src.LatestRelease(ctx)
	} else {
		r, err = releases.Best(ctx, src, scope, stable)
	}
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("selfupdate: %s has no releases", o.Repo)
	}
	return r, nil
}

func stable(v semver.Version) bool { return !v.IsPrerelease() }

// Latest reports the newest stable release, without looking at what it
// contains: no manifest is fetched and nothing is compared with
// o.Current. It is for a program that only wants to know whether there is
// something newer to mention, where Check would cost a second request and
// fail on a release that has no build for this platform.
//
// Drafts, yanked releases and prereleases are never the answer. The version
// is returned without a leading "v" or the module's scope prefix.
func Latest(ctx context.Context, o Options) (string, error) {
	if o.Repo == "" {
		return "", fmt.Errorf("selfupdate: no repository given")
	}
	best, err := releases.Best(ctx, &source{o: o}, discover.Scope{Prefix: o.Prefix}, stable)
	if err != nil {
		return "", err
	}
	if best == nil {
		return "", fmt.Errorf("selfupdate: %s has no stable releases", o.Repo)
	}
	rest, _ := (discover.Scope{Prefix: o.Prefix}).MatchesTag(best.Tag)
	return strings.TrimPrefix(rest, "v"), nil
}

// channelMatch reports whether v is a candidate for channel: any stable
// release is, since a channel always includes stable; a prerelease is only
// when channel is "next" (any prerelease) or names its own identifier.
func channelMatch(v semver.Version, channel string) bool {
	if !v.IsPrerelease() {
		return true
	}
	if channel == "next" {
		return true
	}
	name, _, _ := strings.Cut(v.Prerelease, ".")
	return name == channel
}

func fetchManifest(ctx context.Context, o Options, r *releases.Published) (*manifest.Manifest, error) {
	m, _, err := releases.Manifest(ctx, &source{o: o}, r)
	if releases.IsNoManifest(err) {
		return nil, fmt.Errorf(
			"selfupdate: release %s has no %s, so there is nothing describing what to download\n"+
				"  only releases published by letsgo can be self-updated",
			r.Tag, manifest.FileName)
	}
	if err != nil {
		return nil, fmt.Errorf("selfupdate: %w", err)
	}
	return m, nil
}
