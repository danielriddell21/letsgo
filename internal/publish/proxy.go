package publish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultProxy is the public Go module proxy.
const DefaultProxy = "https://proxy.golang.org"

// WarmProxy asks the module proxy to fetch a freshly tagged version.
//
// Between tagging and the proxy's first fetch there is a window in which
// `go install module@version` fails for a release that plainly exists. To a
// user that is indistinguishable from a broken release, and the usual advice —
// wait a few minutes and try again — is not something a release tool should
// make anyone discover for themselves.
//
// One request closes the window. It is best effort: a proxy that is slow or
// unreachable has not broken anything that was working, so the caller is told
// and the release stands.
func WarmProxy(ctx context.Context, proxy, modulePath, version string) error {
	if proxy == "" {
		proxy = DefaultProxy
	}
	if modulePath == "" || version == "" {
		return fmt.Errorf("publish: module path and version are required")
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	url := fmt.Sprintf("%s/%s/@v/%s.info",
		strings.TrimSuffix(proxy, "/"), EscapeModulePath(modulePath), EscapeModulePath(version))

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	req.Header.Set("User-Agent", "letsgo")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("publish: warming %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("publish: warming %s: %s", url, resp.Status)
	}
	return nil
}

// EscapeModulePath applies the case-encoding the module proxy protocol
// requires.
//
// Module paths are case-sensitive but must address case-insensitive storage,
// so each uppercase letter becomes "!" followed by its lowercase form. Without
// this, github.com/BurntSushi/toml addresses a path the proxy does not have.
func EscapeModulePath(path string) string {
	if !strings.ContainsAny(path, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return path
	}
	var b strings.Builder
	b.Grow(len(path) + 8)
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
