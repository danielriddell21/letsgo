package build

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func binary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCacheRoundTrip(t *testing.T) {
	c := OpenCache(t.TempDir())
	src := binary(t, "compiled output")
	key := CacheKey("commit", "./cmd/foo", "linux/amd64")

	dest := filepath.Join(t.TempDir(), "out")
	if c.Get(key, dest) {
		t.Fatal("an empty cache reported a hit")
	}

	c.Put(key, src)
	if !c.Get(key, dest) {
		t.Fatal("a stored entry was not returned")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "compiled output" {
		t.Errorf("cache returned %q", got)
	}
	// Windows has no executable bit: Go reports 0666 or 0444 there depending
	// only on the read-only attribute, so the assertion is meaningful on
	// systems that model permissions and misleading on the one that does not.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dest); err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Error("the restored binary is not executable")
		}
	}
}

// A cache that can hand back the wrong bytes would quietly break every
// guarantee downstream of it.
func TestCacheRejectsAlteredEntries(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)
	key := CacheKey("commit", "linux/amd64")

	c.Put(key, binary(t, "original"))

	entry := c.path(key)
	if err := os.WriteFile(entry, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "out")
	if c.Get(key, dest) {
		t.Fatal("an altered entry was served as a hit")
	}
	// And the bad entry is gone, so it cannot be tripped over again.
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Error("the altered entry was left in place")
	}
}

// Every input that changes the output must change the key, or a build would
// be reused for something it was not built for.
func TestCacheKeyCoversEveryInput(t *testing.T) {
	base := []string{"commit", "./cmd/foo", "linux/amd64", "-X main.version=1.0.0", "go1.24.7"}
	seen := map[string]string{CacheKey(base...): "base"}

	for i := range base {
		altered := append([]string(nil), base...)
		altered[i] += "-changed"

		key := CacheKey(altered...)
		if previous, clash := seen[key]; clash {
			t.Errorf("changing field %d collides with %s", i, previous)
		}
		seen[key] = "field " + string(rune('0'+i))
	}
}

// Concatenation would make ("ab","c") and ("a","bc") the same build.
func TestCacheKeyIsUnambiguous(t *testing.T) {
	if CacheKey("ab", "c") == CacheKey("a", "bc") {
		t.Error("keys are ambiguous across field boundaries")
	}
}

// Caching is opt-in; an empty key must never hit or store.
func TestEmptyKeyDisablesCaching(t *testing.T) {
	c := OpenCache(t.TempDir())
	c.Put("", binary(t, "x"))
	if c.Get("", filepath.Join(t.TempDir(), "out")) {
		t.Error("an empty key produced a hit")
	}
}

// A cache that cannot be opened should slow builds down, not break them.
func TestNilCacheIsUsable(t *testing.T) {
	var c *Cache
	c.Put("key", binary(t, "x"))
	if c.Get("key", filepath.Join(t.TempDir(), "out")) {
		t.Error("a nil cache reported a hit")
	}
}

// A cached binary must never outlive the compiler that produced it. The
// toolchain is a build input, and an entry keyed without it would be handed
// back to a different Go release as though it were the same bytes.
func TestCacheKeyCoversTheToolchain(t *testing.T) {
	base := []string{"commit", ".", "linux/amd64", "-s -w", "", ""}

	first := CacheKey(append(append([]string{}, base...), "go1.26.8", "", "app")...)
	second := CacheKey(append(append([]string{}, base...), "go1.27.1", "", "app")...)

	if first == second {
		t.Error("two Go versions produced the same cache key")
	}

	// The C compiler determines the bytes exactly as the Go one does, so an
	// entry built with one must never be handed back for a build using another.
	withZig := CacheKey(append(append([]string{}, base...), "go1.27.1", "sha256:aaa", "app")...)
	otherZig := CacheKey(append(append([]string{}, base...), "go1.27.1", "sha256:bbb", "app")...)

	if withZig == otherZig {
		t.Error("two C toolchains produced the same cache key")
	}
	if withZig == second {
		t.Error("a cgo build and a pure-Go build produced the same cache key")
	}
}
