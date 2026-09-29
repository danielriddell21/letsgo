package lsp

import (
	"net/url"
	"runtime"
)

// uriToPath converts a file:// URI, as every LSP client sends, to a local
// filesystem path.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	path := u.Path
	// A Windows path arrives as file:///C:/foo, whose URL.Path is /C:/foo —
	// the leading slash belongs to the URI, not the path.
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return path
}
