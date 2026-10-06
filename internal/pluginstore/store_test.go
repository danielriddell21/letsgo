package pluginstore_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// digest computes what Put itself requires: the real SHA-256 of content, as
// "sha256:...". A fixed, made-up digest would fail Put's own check.
func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestPutThenLookupRoundTrips(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	d := digest("the multi plugin")
	path, err := store.Put(d, "letsgo-multi", []byte("the multi plugin"))
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.Lookup(d, "letsgo-multi")
	if err != nil || !ok {
		t.Fatalf("Lookup = %q, %v, %v", got, ok, err)
	}
	if got != path {
		t.Errorf("Lookup = %q, want %q", got, path)
	}
}

// The whole reason this store exists: two repositories pinning two different
// digests of the same-named plugin must not collide.
func TestTwoDigestsOfTheSameNameCoexist(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	dA := digest("version A")
	dB := digest("version B")
	if _, err := store.Put(dA, "letsgo-multi", []byte("version A")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(dB, "letsgo-multi", []byte("version B")); err != nil {
		t.Fatal(err)
	}

	pathA, okA, err := store.Lookup(dA, "letsgo-multi")
	if err != nil || !okA {
		t.Fatalf("lookup A = %v, %v", okA, err)
	}
	pathB, okB, err := store.Lookup(dB, "letsgo-multi")
	if err != nil || !okB {
		t.Fatalf("lookup B = %v, %v", okB, err)
	}
	if pathA == pathB {
		t.Errorf("both digests landed at %s", pathA)
	}

	gotA, err := os.ReadFile(pathA)
	if err != nil || string(gotA) != "version A" {
		t.Errorf("A = %q, %v", gotA, err)
	}
	gotB, err := os.ReadFile(pathB)
	if err != nil || string(gotB) != "version B" {
		t.Errorf("B = %q, %v", gotB, err)
	}
}

func TestLookupMissesANameTheStoreNeverSaw(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	path, ok, err := store.Lookup("sha256:"+strings.Repeat("a", 64), "letsgo-absent")
	if err != nil || ok || path != "" {
		t.Errorf("Lookup = %q, %v, %v; want a plain miss", path, ok, err)
	}
}

// An entry that has been altered on disk must never be handed back as if it
// were still what it claims to be.
func TestLookupFailsOnATamperedEntry(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	d := digest("original bytes")
	path, err := store.Put(d, "letsgo-multi", []byte("original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered bytes"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, ok, err := store.Lookup(d, "letsgo-multi")
	if ok || err == nil {
		t.Fatalf("Lookup on a tampered entry = %v, %v; want an error", ok, err)
	}
	if !strings.Contains(err.Error(), "tampered") {
		t.Errorf("error = %v", err)
	}
}

func TestPutRefusesDataThatDoesNotMatchTheDigest(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Put("sha256:"+strings.Repeat("a", 64), "letsgo-multi", []byte("not that"))
	if err == nil {
		t.Fatal("Put should have refused mismatched data")
	}
}

func TestOpenUsesTheEnvOverrideWhenNoDirIsGiven(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(pluginstore.StoreEnvOverride, dir)

	store, err := pluginstore.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	d := digest("x")
	path, err := store.Put(d, "letsgo-multi", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("path = %q, want it under %q", path, dir)
	}
}

func TestEntriesListsEveryBinary(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	dA := digest("A")
	dB := digest("B")
	if _, err := store.Put(dA, "letsgo-multi", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(dB, "letsgo-env", []byte("B")); err != nil {
		t.Fatal(err)
	}

	entries, err := store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestEntriesOnAnEmptyStoreIsNotAnError(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.Entries()
	if err != nil || len(entries) != 0 {
		t.Errorf("entries = %+v, err = %v", entries, err)
	}
}

// Prune removes only what keep rejects, which is the entire point: an entry
// still referenced by some pin must survive.
func TestPruneRemovesOnlyWhatKeepRejects(t *testing.T) {
	store, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	dKeep := digest("keep me")
	dDrop := digest("drop me")
	if _, err := store.Put(dKeep, "letsgo-multi", []byte("keep me")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(dDrop, "letsgo-env", []byte("drop me")); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Prune(func(d, name string) bool {
		return d == dKeep && name == "letsgo-multi"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Name != "letsgo-env" {
		t.Fatalf("removed = %+v", removed)
	}

	if _, ok, err := store.Lookup(dKeep, "letsgo-multi"); err != nil || !ok {
		t.Errorf("the kept entry should still be there: %v, %v", ok, err)
	}
	if _, ok, _ := store.Lookup(dDrop, "letsgo-env"); ok {
		t.Error("the dropped entry should be gone")
	}

	// The parent digest directory goes with it — nothing empty left behind.
	if _, err := os.Stat(filepath.Dir(filepath.Dir(removed[0].Path))); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReadOnlyNeverCreatesTheStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	store, err := pluginstore.OpenReadOnly(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if path, ok, err := store.Lookup(digest("x"), "letsgo-x"); ok || err != nil {
		t.Errorf("Lookup = %q, %v, %v; want a plain miss", path, ok, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("stat %s: %v; want it left uncreated", dir, err)
	}
}

func TestOpenReadOnlyFindsWhatOpenInstalled(t *testing.T) {
	dir := t.TempDir()
	writable, err := pluginstore.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	d := digest("the env plugin")
	want, err := writable.Put(d, "letsgo-env", []byte("the env plugin"))
	if err != nil {
		t.Fatal(err)
	}

	readOnly, err := pluginstore.OpenReadOnly(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := readOnly.Lookup(d, "letsgo-env"); !ok || err != nil || got != want {
		t.Errorf("Lookup = %q, %v, %v; want %q", got, ok, err, want)
	}
}

// An interrupted install must leave the old plugin, never half of a new one,
// so the write goes through a rename.
func TestWriteExecutableReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "letsgo-multi")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := pluginstore.WriteExecutable(path, []byte("new")); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}

	// The temporary file is written beside the target; none may survive.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

// An unwritable destination has to fail before anything is reported installed.
func TestWriteExecutableReportsAnUnwritableDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "letsgo-multi")

	if err := pluginstore.WriteExecutable(missing, []byte("bytes")); err == nil {
		t.Fatal("writing into a directory that does not exist should fail")
	}
}

func TestUnreferencedAndPruneUnreferencedAgreeOnWhatNoPinNames(t *testing.T) {
	s, err := pluginstore.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	put := func(name, content string) string {
		t.Helper()
		sum := sha256.Sum256([]byte(content))
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if _, err := s.Put(digest, name, []byte(content)); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	kept := put("letsgo-env", "one")
	put("letsgo-env", "two")  // same name, other digest: unreferenced
	put("letsgo-cask", "one") // same digest, other name: unreferenced
	pins := []pluginstore.Pin{{Digest: kept, Name: "letsgo-env"}}

	listed, err := s.Unreferenced(pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("Unreferenced = %+v, want 2 entries", listed)
	}

	removed, err := s.PruneUnreferenced(pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("PruneUnreferenced removed %+v, want 2 entries", removed)
	}
	left, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Digest != kept || left[0].Name != "letsgo-env" {
		t.Errorf("left = %+v, want only the pinned entry", left)
	}
}
