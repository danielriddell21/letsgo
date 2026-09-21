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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/semver"
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
	if o.Tag == "" && !newer(o.Current, release.TagName) {
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
				release.TagName, o.OS, o.Arch, o.Binary)
		}
		return nil, fmt.Errorf("selfupdate: %s has no %s/%s build", release.TagName, o.OS, o.Arch)
	}

	asset, ok := release.asset(artifact.Name)
	if !ok {
		return nil, fmt.Errorf("selfupdate: %s describes %s but does not attach it",
			release.TagName, artifact.Name)
	}

	return &Update{
		Version:      m.Version,
		Tag:          release.TagName,
		Notes:        release.Body,
		URL:          release.HTMLURL,
		Archive:      artifact.Name,
		SHA256:       artifact.SHA256,
		BinarySHA256: artifact.BinarySHA256,
		Binary:       binaryName(artifact, m.Project, o.OS),
		downloadURL:  asset.BrowserDownloadURL,
		options:      o,
	}, nil
}

// newer reports whether tag is a later version than current.
//
// An unparseable current version — "dev", or a bare commit — is treated as
// older than everything, because a development build asking about updates has
// no claim to being ahead of a release.
func newer(current, tag string) bool {
	next, ok := semver.Parse(tag)
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

type release struct {
	TagName string  `json:"tag_name"`
	Body    string  `json:"body"`
	HTMLURL string  `json:"html_url"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (r *release) asset(name string) (asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return asset{}, false
}

// resolveRelease fetches the release asked for: one exact tag, or the newest.
func resolveRelease(ctx context.Context, o Options) (*release, error) {
	if o.Tag != "" {
		return releaseByTag(ctx, o)
	}
	return latestRelease(ctx, o)
}

func releaseByTag(ctx context.Context, o Options) (*release, error) {
	resp, err := o.get(ctx, fmt.Sprintf("%s/repos/%s/releases/tags/%s", o.api(), o.Repo, url.PathEscape(o.Tag)))
	if err != nil {
		return nil, err
	}
	data, err := read(resp)
	if err != nil {
		return nil, err
	}

	var out release
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("selfupdate: parsing release %s: %w", o.Tag, err)
	}
	if out.TagName == "" {
		return nil, fmt.Errorf("selfupdate: %s has no release %s", o.Repo, o.Tag)
	}
	return &out, nil
}

func latestRelease(ctx context.Context, o Options) (*release, error) {
	resp, err := o.get(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", o.api(), o.Repo))
	if err != nil {
		return nil, err
	}
	data, err := read(resp)
	if err != nil {
		return nil, err
	}

	var out release
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("selfupdate: parsing the latest release: %w", err)
	}
	if out.TagName == "" {
		return nil, fmt.Errorf("selfupdate: %s has no releases", o.Repo)
	}
	return &out, nil
}

func fetchManifest(ctx context.Context, o Options, r *release) (*manifest.Manifest, error) {
	a, ok := r.asset(manifest.FileName)
	if !ok {
		return nil, fmt.Errorf(
			"selfupdate: release %s has no %s, so there is nothing describing what to download\n"+
				"  only releases published by letsgo can be self-updated",
			r.TagName, manifest.FileName)
	}

	resp, err := o.get(ctx, a.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	data, err := read(resp)
	if err != nil {
		return nil, err
	}

	m, err := manifest.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: %w", err)
	}
	return m, nil
}
