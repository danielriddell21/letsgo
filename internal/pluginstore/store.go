// Package pluginstore is the content-addressed store plugin binaries are
// installed into.
//
// A plugin used to be found by name on PATH, which meant only one version of
// it could exist on a machine at a time: two repositories pinning two
// different releases of the same plugin fought over the same file. Storing
// each binary under its own digest removes the collision — the same content
// pinned by ten repositories is written once, and two different contents
// under the same name coexist without conflict, because the name is never
// what decides where a binary lives.
//
// A path in the store is a claim: the file there hashes to the digest in its
// own path. Lookup re-checks that claim on every read, so a store entry that
// has been altered on disk is a failure, never a silent substitution.
package pluginstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/danielriddell21/letsgo/selfupdate"
)

// StoreEnvOverride names a directory to use as the store instead of the
// default location. Nothing reads this today except Open's own fallback, but
// naming it the way LETSGO_GIT and LETSGO_GO are named keeps the family of
// overrides consistent, and gives tests a way to keep the real machine's
// store out of their way.
const StoreEnvOverride = "LETSGO_PLUGIN_STORE"

// Store is the plugin store rooted at a directory.
type Store struct {
	dir string
}

// Open prepares a store under dir. An empty dir uses StoreEnvOverride when
// set, else configured (the global config's `plugins` directive), else the
// default location: $XDG_DATA_HOME/letsgo/plugins, or its platform equivalent.
func Open(dir, configured string) (*Store, error) {
	dir, err := storeDir(dir, configured)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("pluginstore: %w", err)
	}
	return &Store{dir: dir}, nil
}

// OpenReadOnly resolves the store the way Open does but never creates it: a
// reader that only asks whether something is installed — an editor hovering a
// pin — must not leave a directory behind on a machine that has installed
// nothing. A store that does not exist holds nothing, so Lookup reports a
// plain miss.
func OpenReadOnly(dir, configured string) (*Store, error) {
	resolved, err := storeDir(dir, configured)
	if err != nil {
		return nil, err
	}
	return &Store{dir: resolved}, nil
}

// Dir is where the store lives.
func (s *Store) Dir() string { return s.dir }

// storeDir picks where the store lives, without touching the disk.
func storeDir(dir, configured string) (string, error) {
	if dir == "" {
		dir = os.Getenv(StoreEnvOverride)
	}
	if dir == "" {
		dir = configured
	}
	if dir == "" {
		home, err := dataHome()
		if err != nil {
			return "", fmt.Errorf("pluginstore: %w", err)
		}
		dir = filepath.Join(home, "letsgo", "plugins")
	}
	return dir, nil
}

// entryDir is where digest's entries live: sha256/<hex digest>. ok is false
// for anything that is not a "sha256:..." digest, which is never a reason to
// fail a lookup — it just means the store has nothing to say about it.
func (s *Store) entryDir(digest string) (dir string, ok bool) {
	hexDigest, valid := strings.CutPrefix(digest, "sha256:")
	if !valid || hexDigest == "" {
		return "", false
	}
	return filepath.Join(s.dir, "sha256", hexDigest), true
}

// Lookup returns the store path for name at digest, if the store holds it and
// its content still hashes to digest.
//
// ok is false when the store simply has no such entry — the common case,
// worth falling back to PATH for. An entry that exists but no longer hashes
// to its own path is reported as an error instead, never as a plain miss,
// because falling back silently would mean running whatever replaced it.
func (s *Store) Lookup(digest, name string) (path string, ok bool, err error) {
	if name == "" {
		return "", false, nil
	}
	dir, valid := s.entryDir(digest)
	if !valid {
		return "", false, nil
	}

	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", false, nil //nolint:nilerr // no entry is a miss, whatever os.Stat's reason
	}

	got, err := DigestOf(p)
	if err != nil {
		return "", false, fmt.Errorf("pluginstore: reading %s: %w", p, err)
	}
	if got != digest {
		return "", false, fmt.Errorf(
			"pluginstore: %s no longer hashes to %s (it is now %s); refusing to run a tampered entry",
			p, digest, got)
	}
	return p, true, nil
}

// Put installs data into the store at name's digest path, which must be
// data's own digest — the store never decides what a plugin's digest is, it
// only ever confirms one. It returns the store path.
func (s *Store) Put(digest, name string, data []byte) (string, error) {
	if name == "" {
		return "", fmt.Errorf("pluginstore: put needs a name")
	}
	dir, valid := s.entryDir(digest)
	if !valid {
		return "", fmt.Errorf("pluginstore: %q is not a sha256 digest", digest)
	}

	sum := sha256.Sum256(data)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != digest {
		return "", fmt.Errorf("pluginstore: data hashes to %s, not %s", got, digest)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("pluginstore: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := WriteExecutable(path, data); err != nil {
		return "", fmt.Errorf("pluginstore: %w", err)
	}
	return path, nil
}

// Entry is one binary held by the store.
type Entry struct {
	Digest string
	Name   string
	Path   string
}

// Entries lists every binary the store holds.
func (s *Store) Entries() ([]Entry, error) {
	root := filepath.Join(s.dir, "sha256")
	digestDirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("pluginstore: %w", err)
	}

	var entries []Entry
	for _, d := range digestDirs {
		if !d.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			return nil, fmt.Errorf("pluginstore: %w", err)
		}
		for _, n := range names {
			if n.IsDir() {
				continue
			}
			entries = append(entries, Entry{
				Digest: "sha256:" + d.Name(),
				Name:   n.Name(),
				Path:   filepath.Join(root, d.Name(), n.Name()),
			})
		}
	}
	return entries, nil
}

// Pin names a store entry the way a pin in letsgo.mod does: a plugin is only
// ever the same plugin when its digest and its name both agree.
type Pin struct {
	Digest, Name string
}

func pinned(pins []Pin) func(digest, name string) bool {
	set := make(map[Pin]bool, len(pins))
	for _, p := range pins {
		set[p] = true
	}
	return func(digest, name string) bool { return set[Pin{digest, name}] }
}

// Unreferenced lists the entries no pin names: what PruneUnreferenced would
// remove, so pruning is never a surprise.
func (s *Store) Unreferenced(pins []Pin) ([]Entry, error) {
	entries, err := s.Entries()
	if err != nil {
		return nil, err
	}
	keep := pinned(pins)
	var out []Entry
	for _, e := range entries {
		if !keep(e.Digest, e.Name) {
			out = append(out, e)
		}
	}
	return out, nil
}

// PruneUnreferenced removes every entry no pin names, and returns what was
// removed.
func (s *Store) PruneUnreferenced(pins []Pin) ([]Entry, error) {
	return s.Prune(pinned(pins))
}

// Fetch resolves the release options names, downloads and checks it, and
// installs the executable into the store at its own digest. It returns the
// release, the binary and where the binary now is. A repository with no
// releases is an error.
func (s *Store) Fetch(ctx context.Context, options selfupdate.Options) (release *selfupdate.Update, binary []byte, path string, err error) {
	release, err = selfupdate.Check(ctx, options)
	if err != nil {
		return nil, nil, "", err
	}
	if release == nil {
		return nil, nil, "", fmt.Errorf("pluginstore: %s has no releases", options.Repo)
	}

	binary, err = release.Download(ctx)
	if err != nil {
		return nil, nil, "", err
	}

	path, err = s.Put("sha256:"+release.BinarySHA256, release.Binary, binary)
	if err != nil {
		return nil, nil, "", err
	}
	return release, binary, path, nil
}

// Prune removes every entry keep reports false for, and returns what was
// removed.
func (s *Store) Prune(keep func(digest, name string) bool) ([]Entry, error) {
	entries, err := s.Entries()
	if err != nil {
		return nil, err
	}

	var removed []Entry
	for _, e := range entries {
		if keep(e.Digest, e.Name) {
			continue
		}
		// Only this name's file: the same digest can hold other names, and one
		// of those may be pinned.
		if err := os.Remove(e.Path); err != nil {
			return removed, fmt.Errorf("pluginstore: removing %s: %w", e.Path, err)
		}
		// Tidy the digest's directory once it holds nothing; a failure here
		// means a sibling is still in it, which is exactly the case to keep.
		_ = os.Remove(filepath.Dir(e.Path))
		removed = append(removed, e)
	}
	return removed, nil
}

// DigestOf is the SHA-256 of the file at path, as "sha256:…".
//
// The one place a plugin's digest is computed, so that anything reporting on a
// pin (plugin.DigestOf) and the store's own check of its entries give the
// same answer to the question the pin exists to settle.
func DigestOf(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("pluginstore: reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", fmt.Errorf("pluginstore: reading %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil)), nil
}

// WriteExecutable writes data to path through a temporary file beside it, so
// an interrupted write leaves either the old content or none, never half of
// the new content. Beside it rather than in a system temp directory, because
// a rename across filesystems is a copy and stops being atomic.
func WriteExecutable(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("pluginstore: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pluginstore: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pluginstore: %w", err)
	}
	if err := os.Chmod(name, 0o755); err != nil { //nolint:gosec // a plugin must be executable
		return fmt.Errorf("pluginstore: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("pluginstore: %w", err)
	}
	return nil
}

// dataHome is $XDG_DATA_HOME, or its default per platform when the
// environment variable is unset: the convention XDG itself defines for
// Linux, and each platform's own native equivalent otherwise — the same
// reasoning os.UserConfigDir uses, which the global config file due in a
// later phase relies on directly.
func dataHome() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pluginstore: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	default:
		return filepath.Join(home, ".local", "share"), nil
	}
}
