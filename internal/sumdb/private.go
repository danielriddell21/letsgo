package sumdb

import (
	"os"
	"path"
	"strings"
)

// PrivateModule reports whether modulePath is excluded from sumdb checks by
// GOPRIVATE, GONOSUMDB or GONOSUMCHECK, and which one matched (SD-7).
func PrivateModule(modulePath string) (skip bool, reason string) {
	for _, name := range []string{"GONOSUMCHECK", "GONOSUMDB", "GOPRIVATE"} {
		if matchPrefixPatterns(os.Getenv(name), modulePath) {
			return true, name
		}
	}
	return false, ""
}

// matchPrefixPatterns reports whether target matches any comma-separated
// glob in patterns, comparing each glob against the same number of leading
// path elements of target — the same rule cmd/go uses for GOPRIVATE and
// friends (golang.org/x/mod/module.MatchPrefixPatterns), reproduced here
// since letsgo has no dependencies to import it from.
func matchPrefixPatterns(patterns, target string) bool {
	for patterns != "" {
		var glob string
		if i := strings.Index(patterns, ","); i >= 0 {
			glob, patterns = patterns[:i], patterns[i+1:]
		} else {
			glob, patterns = patterns, ""
		}
		glob = strings.TrimSpace(glob)
		if glob == "" {
			continue
		}

		n := strings.Count(glob, "/")
		prefix := target
		for i := 0; i < len(target); i++ {
			if target[i] == '/' {
				if n == 0 {
					prefix = target[:i]
					break
				}
				n--
			}
		}
		if n > 0 {
			continue
		}
		if matched, _ := path.Match(glob, prefix); matched {
			return true
		}
	}
	return false
}
