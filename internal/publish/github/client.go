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

// CheckToken verifies the token can write to the repository, in one call,
// before anything expensive happens.
func (c *Client) CheckToken(ctx context.Context, repo Repo) error {
	var result struct {
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
		Archived bool `json:"archived"`
	}

	url := fmt.Sprintf("%s/repos/%s", c.api, repo)
	if err := c.do(ctx, http.MethodGet, url, nil, "", &result); err != nil {
		if NotFound(err) {
			return fmt.Errorf("github: %s is not visible to this token; it may be private, renamed, or the token may lack the repo scope", repo)
		}
		return err
	}
	if result.Archived {
		return fmt.Errorf("github: %s is archived and cannot receive a release", repo)
	}
	if !result.Permissions.Push {
		return fmt.Errorf("github: this token cannot write to %s; a release needs contents:write", repo)
	}
	return nil
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
