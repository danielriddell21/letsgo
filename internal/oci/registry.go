package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxBlob bounds what will be read from a registry. An image built from a Go
// binary is tens of megabytes; anything past this is a hostile or broken
// endpoint, and reading it would be a memory exhaustion waiting to happen.
const maxBlob = 512 << 20

// Registry talks to one OCI registry.
//
// Enough of the distribution API to push an image and to read a base: four
// verbs, no SDK, no daemon. The token dance is the only part with any
// subtlety, and it is the same on every registry because the specification
// says so.
type Registry struct {
	Host string

	// Username and Password authenticate to the token endpoint. Both empty
	// means anonymous, which is how public base images are read.
	Username string
	Password string

	// Scheme is "https" everywhere but in tests.
	Scheme string

	HTTP      *http.Client
	UserAgent string

	mu     sync.Mutex
	tokens map[string]string
}

// NewRegistry returns a client for host.
func NewRegistry(host string) *Registry {
	return &Registry{
		Host:      host,
		Scheme:    "https",
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
		UserAgent: "letsgo",
		tokens:    map[string]string{},
	}
}

// Error is a non-success response from a registry.
type Error struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("oci: %s %s: %d", e.Method, e.URL, e.StatusCode)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func (r *Registry) url(format string, args ...any) string {
	return r.Scheme + "://" + r.Host + fmt.Sprintf(format, args...)
}

// do sends a request, obtaining a bearer token first if the registry asks for
// one, and retrying exactly once with it.
//
// One retry, not a loop: a registry that answers 401 to a request already
// carrying the token it just issued is not going to be talked round.
func (r *Registry) do(ctx context.Context, req *http.Request, scope string, body func() io.Reader) (*http.Response, error) {
	r.apply(req, scope)

	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oci: %s %s: %w", req.Method, req.URL, err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	resp.Body.Close()

	if err := r.authenticate(ctx, challenge, scope); err != nil {
		return nil, err
	}

	retry := req.Clone(ctx)
	if body != nil {
		retry.Body = io.NopCloser(body())
	}
	r.apply(retry, scope)

	resp, err = r.HTTP.Do(retry)
	if err != nil {
		return nil, fmt.Errorf("oci: %s %s: %w", retry.Method, retry.URL, err)
	}
	return resp, nil
}

func (r *Registry) apply(req *http.Request, scope string) {
	req.Header.Set("User-Agent", r.UserAgent)

	r.mu.Lock()
	token := r.tokens[scope]
	r.mu.Unlock()

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// authenticate fetches a bearer token for scope from the realm the registry
// named in its challenge.
func (r *Registry) authenticate(ctx context.Context, challenge, scope string) error {
	params := parseChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		return fmt.Errorf("oci: %s refused the request and named no token endpoint", r.Host)
	}

	endpoint, err := url.Parse(realm)
	if err != nil {
		return fmt.Errorf("oci: %s named an unusable token endpoint %q: %w", r.Host, realm, err)
	}
	query := endpoint.Query()
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("oci: %w", err)
	}
	req.Header.Set("User-Agent", r.UserAgent)
	if r.Username != "" || r.Password != "" {
		req.SetBasicAuth(r.Username, r.Password)
	}

	resp, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("oci: requesting a token from %s: %w", endpoint.Host, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("oci: reading token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{StatusCode: resp.StatusCode, Method: req.Method, URL: endpoint.String(),
			Body: strings.TrimSpace(string(data))}
	}

	var issued struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &issued); err != nil {
		return fmt.Errorf("oci: parsing token response from %s: %w", endpoint.Host, err)
	}

	token := issued.Token
	if token == "" {
		// Some registries answer with access_token and no token, which the
		// specification permits.
		token = issued.AccessToken
	}
	if token == "" {
		return fmt.Errorf("oci: %s issued an empty token", endpoint.Host)
	}

	r.mu.Lock()
	r.tokens[scope] = token
	r.mu.Unlock()
	return nil
}

// parseChallenge reads the key="value" pairs out of a WWW-Authenticate header.
func parseChallenge(header string) map[string]string {
	out := map[string]string{}
	_, params, ok := strings.Cut(header, " ")
	if !ok {
		return out
	}
	for _, part := range splitOutsideQuotes(params) {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.ToLower(key)] = strings.Trim(value, `"`)
	}
	return out
}

// splitOutsideQuotes splits on commas that are not inside a quoted value,
// because a scope can legitimately contain one.
func splitOutsideQuotes(s string) []string {
	var parts []string
	var current strings.Builder
	quoted := false

	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == ',' && !quoted:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func pullScope(repo string) string { return "repository:" + repo + ":pull" }
func pushScope(repo string) string { return "repository:" + repo + ":pull,push" }

// read drains and closes a response body, bounded.
func read(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBlob))
	if err != nil {
		return nil, fmt.Errorf("oci: reading response: %w", err)
	}
	return data, nil
}

func errorFrom(req *http.Request, resp *http.Response, body []byte) error {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		text = text[:500] + "…"
	}
	return &Error{StatusCode: resp.StatusCode, Method: req.Method, URL: req.URL.String(), Body: text}
}

// HasBlob reports whether the repository already holds a blob.
func (r *Registry) HasBlob(ctx context.Context, repo string, digest Digest) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		r.url("/v2/%s/blobs/%s", repo, digest), nil)
	if err != nil {
		return false, fmt.Errorf("oci: %w", err)
	}

	resp, err := r.do(ctx, req, pushScope(repo), nil)
	if err != nil {
		return false, err
	}
	body, err := read(resp)
	if err != nil {
		return false, err
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	default:
		return false, errorFrom(req, resp, body)
	}
}

// PushBlob uploads a blob, skipping the transfer when the registry already has
// it. Layers are content-addressed, so a re-run of a release uploads nothing.
func (r *Registry) PushBlob(ctx context.Context, repo string, blob Blob) error {
	present, err := r.HasBlob(ctx, repo, blob.Digest)
	if err != nil {
		return err
	}
	if present {
		return nil
	}

	location, err := r.startUpload(ctx, repo, "")
	if err != nil {
		return err
	}
	if location == "" {
		// Mounted from elsewhere; nothing left to send.
		return nil
	}
	return r.finishUpload(ctx, repo, location, blob)
}

// startUpload opens an upload session and returns the URL to complete it at.
// An empty URL means the blob was mounted from another repository instead.
func (r *Registry) startUpload(ctx context.Context, repo, mount string) (string, error) {
	endpoint := r.url("/v2/%s/blobs/uploads/", repo)
	if mount != "" {
		endpoint += "?" + mount
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("oci: %w", err)
	}
	req.Header.Set("Content-Length", "0")

	resp, err := r.do(ctx, req, pushScope(repo), nil)
	if err != nil {
		return "", err
	}
	body, err := read(resp)
	if err != nil {
		return "", err
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		// The registry had the blob already and mounted it.
		return "", nil
	case http.StatusAccepted:
		location := resp.Header.Get("Location")
		if location == "" {
			return "", fmt.Errorf("oci: %s accepted an upload without saying where to send it", r.Host)
		}
		return r.absolute(location), nil
	default:
		return "", errorFrom(req, resp, body)
	}
}

// absolute resolves a Location header, which the specification permits to be
// relative to the registry root.
func (r *Registry) absolute(location string) string {
	if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		return location
	}
	if !strings.HasPrefix(location, "/") {
		location = "/" + location
	}
	return r.Scheme + "://" + r.Host + location
}

func (r *Registry) finishUpload(ctx context.Context, repo, location string, blob Blob) error {
	endpoint, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("oci: %s named an unusable upload location %q: %w", r.Host, location, err)
	}
	query := endpoint.Query()
	query.Set("digest", string(blob.Digest))
	endpoint.RawQuery = query.Encode()

	body := func() io.Reader { return bytes.NewReader(blob.Content) }

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), body())
	if err != nil {
		return fmt.Errorf("oci: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = blob.Size()

	resp, err := r.do(ctx, req, pushScope(repo), body)
	if err != nil {
		return err
	}
	data, err := read(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return errorFrom(req, resp, data)
	}
	return nil
}

// PushManifest publishes a manifest or an index under a tag or digest.
func (r *Registry) PushManifest(ctx context.Context, repo, target string, blob Blob) error {
	body := func() io.Reader { return bytes.NewReader(blob.Content) }

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		r.url("/v2/%s/manifests/%s", repo, target), body())
	if err != nil {
		return fmt.Errorf("oci: %w", err)
	}
	req.Header.Set("Content-Type", blob.MediaType)
	req.ContentLength = blob.Size()

	resp, err := r.do(ctx, req, pushScope(repo), body)
	if err != nil {
		return err
	}
	data, err := read(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return errorFrom(req, resp, data)
	}
	return nil
}

// acceptManifests asks for every manifest shape letsgo can read. A registry
// that is not told will hand back a schema 1 manifest.
var acceptManifests = strings.Join([]string{
	MediaTypeIndex, MediaTypeManifest, MediaTypeDockerList, MediaTypeDockerMani,
}, ", ")

// Fetched is a manifest as the registry returned it.
type Fetched struct {
	MediaType string
	Digest    Digest
	Content   []byte
}

// Manifest reads a manifest or index.
func (r *Registry) Manifest(ctx context.Context, repo, target string) (*Fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.url("/v2/%s/manifests/%s", repo, target), nil)
	if err != nil {
		return nil, fmt.Errorf("oci: %w", err)
	}
	req.Header.Set("Accept", acceptManifests)

	resp, err := r.do(ctx, req, pullScope(repo), nil)
	if err != nil {
		return nil, err
	}
	mediaType := resp.Header.Get("Content-Type")

	data, err := read(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errorFrom(req, resp, data)
	}

	// The digest is recomputed rather than read from the header: it is the
	// name of what we actually received, and a registry that disagrees with
	// its own bytes is not something to take on trust.
	return &Fetched{MediaType: mediaType, Digest: DigestOf(data), Content: data}, nil
}

// Blob downloads a blob and checks it against the digest that named it.
func (r *Registry) Blob(ctx context.Context, repo string, digest Digest) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.url("/v2/%s/blobs/%s", repo, digest), nil)
	if err != nil {
		return nil, fmt.Errorf("oci: %w", err)
	}

	resp, err := r.do(ctx, req, pullScope(repo), nil)
	if err != nil {
		return nil, err
	}
	data, err := read(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errorFrom(req, resp, data)
	}
	if got := DigestOf(data); got != digest {
		return nil, fmt.Errorf("oci: %s/%s is %s, not the blob that was asked for", r.Host, repo, got)
	}
	return data, nil
}
