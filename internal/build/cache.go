package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cache stores compiled binaries under a key derived from their inputs.
//
// Go's own build cache already makes recompilation incremental; this one
// removes the compile entirely when nothing that feeds it has changed, which
// is what makes a retried or resumed release cost seconds rather than minutes.
//
// Two rules keep it from undermining the thing it is speeding up. A cached
// binary is re-hashed before use, so a truncated or altered entry is a miss
// rather than a silent substitution. And caching is opt-in per build: the
// caller supplies a key only when it knows the inputs are fully described by
// one — which means a clean worktree at a known commit, and never a
// verification, where reusing a previous build would verify nothing.
type Cache struct {
	dir string
}

// OpenCache prepares a cache under dir. An empty dir uses the user cache
// directory. A cache that cannot be opened is not an error: builds simply
// happen the slow way.
func OpenCache(dir string) *Cache {
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil
		}
		dir = filepath.Join(base, "letsgo", "builds")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &Cache{dir: dir}
}

// CacheKey derives an entry key from everything that determines the output.
//
// Anything omitted here is something the cache would be blind to, so the
// inputs are spelled out at the call site rather than gathered implicitly.
func CacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		// Length-prefixed so that ("ab", "c") and ("a", "bc") differ.
		fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cache) path(key string) string {
	if c == nil {
		return ""
	}
	// Sharded so a long-lived cache does not become one enormous directory.
	return filepath.Join(c.dir, key[:2], key)
}

// Get copies a cached binary to dest, reporting whether one was used.
func (c *Cache) Get(key, dest string) bool {
	if c == nil || key == "" {
		return false
	}

	src := c.path(key)
	data, err := os.ReadFile(src)
	if err != nil {
		return false
	}

	// The key describes the inputs; this checks the entry still holds what
	// those inputs produced. A cache that can hand back the wrong bytes would
	// quietly break every guarantee downstream of it.
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != readDigest(src+".sha256") {
		_ = os.Remove(src)
		_ = os.Remove(src + ".sha256")
		return false
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return false
	}
	// A cached binary has to come back executable, so this cannot be the
	// 0600 gosec wants: 0700 is as tight as an executable gets.
	if err := os.WriteFile(dest, data, 0o700); err != nil { //nolint:gosec // an executable must keep its x bit
		return false
	}
	return true
}

// Put stores a freshly built binary. Failure to store is not reported: a
// cache that cannot be written is slower, not wrong.
func (c *Cache) Put(key, src string) {
	if c == nil || key == "" {
		return
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return
	}

	dest := c.path(key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return
	}

	// Written through a temporary file so an interrupted write cannot leave a
	// partial entry that later reads would have to catch.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".tmp-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}

	sum := sha256.Sum256(data)
	if err := os.WriteFile(dest+".sha256", []byte(hex.EncodeToString(sum[:])), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), dest)
}

func readDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
