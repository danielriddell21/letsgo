package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DownloadURL is the public address of a release asset. Release assets are
// served from a predictable path, so a URL for something just uploaded needs
// no second round trip to discover.
func DownloadURL(repo Repo, tag, name string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		repo.Owner, repo.Name, url.PathEscape(tag), url.PathEscape(name))
}

// RepoInfo is what a repository says about itself. A generated Homebrew
// formula needs a description and a licence, and asking the forge is better
// than adding two config directives for facts that already exist.
type RepoInfo struct {
	Description string
	Homepage    string

	// License is the SPDX identifier GitHub detected, e.g. "MIT". Empty when
	// the repository has no recognised licence.
	License string
}

// Repository reads a repository's description and licence.
func (c *Client) Repository(ctx context.Context, repo Repo) (*RepoInfo, error) {
	var result struct {
		Description string `json:"description"`
		Homepage    string `json:"homepage"`
		License     *struct {
			SPDXID string `json:"spdx_id"`
		} `json:"license"`
	}

	url := fmt.Sprintf("%s/repos/%s", c.api, repo)
	if err := c.do(ctx, http.MethodGet, url, nil, "", &result); err != nil {
		return nil, err
	}

	info := &RepoInfo{Description: result.Description, Homepage: result.Homepage}
	// GitHub reports "NOASSERTION" for a licence file it cannot identify,
	// which is not something to put in a formula.
	if result.License != nil && result.License.SPDXID != "NOASSERTION" {
		info.License = result.License.SPDXID
	}
	return info, nil
}

// File is a file in a repository.
type File struct {
	Path string

	// SHA is the blob's identifier. Updating a file requires it, and that is
	// the mechanism that makes a write fail rather than silently clobber
	// somebody else's change made in between.
	SHA string

	Content []byte
}

// ReadFile fetches a file's contents. A missing file returns nil without an
// error: "not there yet" is the normal state of a formula's first publication.
func (c *Client) ReadFile(ctx context.Context, repo Repo, path string) (*File, error) {
	var result struct {
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}

	url := fmt.Sprintf("%s/repos/%s/contents/%s", c.api, repo, escapePath(path))
	if err := c.do(ctx, http.MethodGet, url, nil, "", &result); err != nil {
		if NotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	if result.Encoding != "base64" {
		return nil, fmt.Errorf("github: %s/%s came back as %q, which this client cannot decode",
			repo, path, result.Encoding)
	}
	// The API wraps base64 at 60 columns, which the strict decoder rejects.
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(result.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("github: decoding %s/%s: %w", repo, path, err)
	}
	return &File{Path: path, SHA: result.SHA, Content: content}, nil
}

// FileInput describes a file to write.
type FileInput struct {
	Path    string
	Message string
	Content []byte

	// SHA is the blob being replaced. Empty creates the file, and the write
	// then fails if it already exists.
	SHA string
}

// WriteFile creates or replaces a file, as a commit on the default branch.
func (c *Client) WriteFile(ctx context.Context, repo Repo, in FileInput) error {
	body := struct {
		Message string `json:"message"`
		Content string `json:"content"`
		SHA     string `json:"sha,omitempty"`
	}{
		Message: in.Message,
		Content: base64.StdEncoding.EncodeToString(in.Content),
		SHA:     in.SHA,
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/contents/%s", c.api, repo, escapePath(in.Path))
	return c.do(ctx, http.MethodPut, url, bytes.NewReader(encoded), "application/json", nil)
}

// escapePath escapes a repository path for a URL while leaving the separators
// alone, since url.PathEscape would turn them into %2F and address a file
// whose name contains slashes rather than a file in a directory.
func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
