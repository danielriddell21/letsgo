// Package github publishes releases to GitHub.
//
// It speaks to the REST API directly rather than through an SDK. Roughly eight
// endpoints are needed, and a release tool that signs and publishes your
// artifacts is a poor place to inherit a large dependency tree: its attack
// surface is part of its threat model.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultAPI    = "https://api.github.com"
	defaultUpload = "https://uploads.github.com"

	// The version header pins the API's behaviour. Without it GitHub is free
	// to change response shapes under us.
	apiVersion = "2022-11-28"
)

// Client talks to the GitHub REST API.
type Client struct {
	token  string
	api    string
	upload string
	http   *http.Client

	// UserAgent identifies the tool. GitHub requires one.
	UserAgent string
}

// New returns a client authenticated with token.
func New(token string) *Client {
	return &Client{
		token:     token,
		api:       defaultAPI,
		upload:    defaultUpload,
		http:      &http.Client{Timeout: 5 * time.Minute},
		UserAgent: "letsgo",
	}
}

// SetEndpoints overrides the API hosts. Used by tests, and by GitHub
// Enterprise installations.
func (c *Client) SetEndpoints(api, upload string) {
	c.api, c.upload = strings.TrimSuffix(api, "/"), strings.TrimSuffix(upload, "/")
}

// Repo identifies a repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Release is a GitHub release.
type Release struct {
	ID         int64   `json:"id"`
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	HTMLURL    string  `json:"html_url"`
	Assets     []Asset `json:"assets"`
}

// Asset finds a file attached to the release by name.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Asset is a file attached to a release.
type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`

	// Digest is "sha256:<hex>" when GitHub reports one. It is the difference
	// between knowing an upload is correct and assuming it, so where it is
	// absent the size is the only check available.
	Digest string `json:"digest"`
}

// SHA256 returns the asset's digest without its algorithm prefix, and whether
// GitHub reported one at all.
func (a Asset) SHA256() (string, bool) {
	hex, ok := strings.CutPrefix(a.Digest, "sha256:")
	return hex, ok && hex != ""
}

// ReleaseInput describes a release to create or update.
type ReleaseInput struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name,omitempty"`
	Body       string `json:"body,omitempty"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`

	// TargetCommitish pins the release to a commit when the tag does not yet
	// exist on the remote.
	TargetCommitish string `json:"target_commitish,omitempty"`
}

// APIError is a non-success response from GitHub.
type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Message    string
	Errors     []string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "github: %s %s: %d", e.Method, e.URL, e.StatusCode)
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	for _, detail := range e.Errors {
		fmt.Fprintf(&b, "\n  %s", detail)
	}
	return b.String()
}

// NotFound reports whether the error is a 404, which for several of these
// calls is an ordinary answer rather than a failure.
func NotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// ReleaseByTag finds an existing release. A missing release returns nil
// without an error: "not released yet" is the normal state of a first run.
func (c *Client) ReleaseByTag(ctx context.Context, repo Repo, tag string) (*Release, error) {
	var release Release
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", c.api, repo, tag)

	err := c.do(ctx, http.MethodGet, url, nil, "", &release)
	if NotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &release, nil
}

// CreateRelease creates a release.
func (c *Client) CreateRelease(ctx context.Context, repo Repo, in ReleaseInput) (*Release, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("github: encoding release: %w", err)
	}

	var release Release
	url := fmt.Sprintf("%s/repos/%s/releases", c.api, repo)
	if err := c.do(ctx, http.MethodPost, url, bytes.NewReader(body), "application/json", &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// UpdateRelease edits an existing release.
func (c *Client) UpdateRelease(ctx context.Context, repo Repo, id int64, in ReleaseInput) (*Release, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("github: encoding release: %w", err)
	}

	var release Release
	url := fmt.Sprintf("%s/repos/%s/releases/%d", c.api, repo, id)
	if err := c.do(ctx, http.MethodPatch, url, bytes.NewReader(body), "application/json", &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// Assets lists a release's attached files.
func (c *Client) Assets(ctx context.Context, repo Repo, releaseID int64) ([]Asset, error) {
	var assets []Asset
	page := 1

	for {
		var batch []Asset
		url := fmt.Sprintf("%s/repos/%s/releases/%d/assets?per_page=100&page=%d", c.api, repo, releaseID, page)
		if err := c.do(ctx, http.MethodGet, url, nil, "", &batch); err != nil {
			return nil, err
		}
		assets = append(assets, batch...)

		// A release with more than a hundred assets is unusual but not
		// impossible, and a truncated list would make resume re-upload
		// everything past the first page.
		if len(batch) < 100 {
			return assets, nil
		}
		page++
	}
}

// DeleteAsset removes an attached file.
func (c *Client) DeleteAsset(ctx context.Context, repo Repo, assetID int64) error {
	url := fmt.Sprintf("%s/repos/%s/releases/assets/%d", c.api, repo, assetID)
	return c.do(ctx, http.MethodDelete, url, nil, "", nil)
}

// UploadAsset attaches a file to a release.
func (c *Client) UploadAsset(ctx context.Context, repo Repo, releaseID int64, name string, size int64, content io.Reader) (*Asset, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/%d/assets?name=%s",
		c.upload, repo, releaseID, urlQueryEscape(name))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, content)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/octet-stream")

	// Set explicitly so the upload is not chunked: GitHub rejects a chunked
	// asset upload.
	req.ContentLength = size

	var asset Asset
	if err := c.send(req, &asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// Access describes what a token may do with a repository.
//
// Reported rather than judged: what counts as sufficient depends on the
// operation, and how to obtain it depends on where the token came from.
// Both are the caller's business.
type Access struct {
	// CanPush reports write access as the API describes it.
	CanPush bool

	// Archived repositories accept no writes at all.
	Archived bool
}

// CheckAccess reads what this token may do with the repository, in one call.
func (c *Client) CheckAccess(ctx context.Context, repo Repo) (Access, error) {
	var result struct {
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
		Archived bool `json:"archived"`
	}

	url := fmt.Sprintf("%s/repos/%s", c.api, repo)
	if err := c.do(ctx, http.MethodGet, url, nil, "", &result); err != nil {
		if NotFound(err) {
			return Access{}, fmt.Errorf(
				"github: %s is not visible to this token; it may be private, renamed, or the token may lack access",
				repo)
		}
		return Access{}, err
	}

	return Access{CanPush: result.Permissions.Push, Archived: result.Archived}, nil
}

func (c *Client) do(ctx context.Context, method, url string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	c.setHeaders(req)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.send(req, out)
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", c.UserAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *Client) send(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", req.Method, req.URL, err)
	}
	defer resp.Body.Close()

	// Bounded so a hostile or broken endpoint cannot exhaust memory.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("github: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseError(req, resp, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("github: parsing response from %s: %w", req.URL, err)
	}
	return nil
}

func parseError(req *http.Request, resp *http.Response, data []byte) error {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Method:     req.Method,
		URL:        req.URL.String(),
	}

	var payload struct {
		Message string `json:"message"`
		Errors  []struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &payload) == nil {
		apiErr.Message = payload.Message
		for _, e := range payload.Errors {
			switch {
			case e.Message != "":
				apiErr.Errors = append(apiErr.Errors, e.Message)
			case e.Field != "":
				apiErr.Errors = append(apiErr.Errors, fmt.Sprintf("%s.%s: %s", e.Resource, e.Field, e.Code))
			}
		}
	}

	// Rate limiting is worth naming, because the fix is to wait rather than to
	// change anything.
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		apiErr.Message = "rate limit exceeded; " + apiErr.Message
	}
	return apiErr
}

// urlQueryEscape escapes an asset name for use in a query parameter. Asset
// names are filenames, which may contain characters that would otherwise end
// the parameter early.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteByte(r)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", r)
	}
	return b.String()
}

// Tag is a git tag as the API reports it.
type Tag struct {
	Name string `json:"name"`
}

// Tags lists the repository's tags.
//
// The API returns them in its own order, which is not version order, so
// callers must sort rather than take the first.
func (c *Client) Tags(ctx context.Context, repo Repo, limit int) ([]string, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	var tags []Tag
	url := fmt.Sprintf("%s/repos/%s/tags?per_page=%d", c.api, repo, limit)
	if err := c.do(ctx, http.MethodGet, url, nil, "", &tags); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	return names, nil
}

// CommitInfo is one commit from the compare endpoint.
type CommitInfo struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

// Compare lists the commits between two revisions, oldest first.
//
// This is the path taken when the checkout is shallow and the history simply
// is not present locally. Demanding a full clone instead — the usual advice,
// and what fetch-depth: 0 exists for — makes every CI run slower forever to
// serve one step of one job.
func (c *Client) Compare(ctx context.Context, repo Repo, base, head string) ([]CommitInfo, error) {
	var result struct {
		Commits []CommitInfo `json:"commits"`
	}

	url := fmt.Sprintf("%s/repos/%s/compare/%s...%s", c.api, repo, base, head)
	if err := c.do(ctx, http.MethodGet, url, nil, "", &result); err != nil {
		return nil, err
	}
	return result.Commits, nil
}

// maxCommitPages bounds history walks. A hundred commits per page over ten
// pages is far more than release notes can usefully present, and an unbounded
// walk over a long-lived repository would spend a rate limit to produce
// something nobody reads.
const maxCommitPages = 10

// CommitsUpTo lists the commits reachable from ref, newest first.
//
// Used where Compare cannot be: a first release has no earlier tag to compare
// against, and the compare endpoint requires a base.
func (c *Client) CommitsUpTo(ctx context.Context, repo Repo, ref string) ([]CommitInfo, error) {
	var commits []CommitInfo

	for page := 1; page <= maxCommitPages; page++ {
		var batch []CommitInfo
		url := fmt.Sprintf("%s/repos/%s/commits?sha=%s&per_page=100&page=%d",
			c.api, repo, urlQueryEscape(ref), page)

		if err := c.do(ctx, http.MethodGet, url, nil, "", &batch); err != nil {
			return nil, err
		}
		commits = append(commits, batch...)

		if len(batch) < 100 {
			break
		}
	}
	return commits, nil
}

// ErrIndeterminate reports that the forge's answer established neither
// permission nor its absence.
var ErrIndeterminate = errors.New("github: permission could not be determined")

// CanCreateRelease reports whether this token may create releases in repo.
//
// Installation tokens — the kind GitHub Actions provides — cannot be
// introspected: no endpoint describes what one is permitted to do, and the
// permissions field on a repository describes the authenticated user, of which
// an installation token has none. Asking is the only interface available.
//
// So it asks, with a request that cannot succeed: a creation missing its tag
// name. Authorisation is decided before the body is validated, which separates
// the two answers — 403 for a token that may not create releases, 422 for one
// that may and simply sent nonsense. Nothing is created either way.
//
// The ordering of those two checks is observed behaviour rather than a
// documented guarantee, so anything else is reported as ErrIndeterminate
// rather than guessed at.
func (c *Client) CanCreateRelease(ctx context.Context, repo Repo) (bool, error) {
	body, err := json.Marshal(ReleaseInput{})
	if err != nil {
		return false, fmt.Errorf("github: encoding probe: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/releases", c.api, repo)
	err = c.do(ctx, http.MethodPost, url, strings.NewReader(string(body)), "application/json", nil)

	// A probe that succeeds has created a release, which it was built not to
	// do. Report that rather than pretend the answer is clean.
	if err == nil {
		return true, fmt.Errorf("github: release-write probe unexpectedly succeeded against %s", repo)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false, err
	}

	switch apiErr.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized:
		return false, nil
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return true, nil
	default:
		return false, fmt.Errorf("%w: %s", ErrIndeterminate, apiErr)
	}
}

// DownloadAsset fetches a release asset's content.
//
// The asset API returns metadata by default and the file itself only when
// asked for a media type it cannot represent as JSON.
func (c *Client) DownloadAsset(ctx context.Context, repo Repo, assetID int64) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/assets/%d", c.api, repo, assetID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: downloading asset %d: %w", assetID, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("github: reading asset %d: %w", assetID, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, parseError(req, resp, data)
	}
	return data, nil
}

// LatestRelease returns the most recent published release, or nil when the
// repository has none. Drafts and prereleases are excluded by the forge.
func (c *Client) LatestRelease(ctx context.Context, repo Repo) (*Release, error) {
	var release Release
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.api, repo)

	err := c.do(ctx, http.MethodGet, url, nil, "", &release)
	if NotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &release, nil
}

// Attestation is a signed statement about how an artifact was produced.
type Attestation struct {
	Bundle struct {
		MediaType            string `json:"mediaType"`
		VerificationMaterial struct {
			Certificate struct {
				RawBytes string `json:"rawBytes"`
			} `json:"certificate"`
		} `json:"verificationMaterial"`
	} `json:"bundle"`
	RepositoryID int64 `json:"repository_id"`
}

// Attestations returns the provenance recorded for an artifact digest.
//
// An empty result is not an error: most releases have none, and saying so is
// more useful than failing.
func (c *Client) Attestations(ctx context.Context, repo Repo, sha256Hex string) ([]Attestation, error) {
	var result struct {
		Attestations []Attestation `json:"attestations"`
	}

	url := fmt.Sprintf("%s/repos/%s/attestations/sha256:%s", c.api, repo, sha256Hex)
	err := c.do(ctx, http.MethodGet, url, nil, "", &result)
	if NotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return result.Attestations, nil
}
