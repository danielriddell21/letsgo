package release

import (
	"fmt"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/manifest"
)

// Digest is the sha256 of one of the release's files as it is on disk now.
// The manifest records no digest of itself and a stamp rewrites it, so this is
// the one place a file's digest is asked for.
func (r *Result) Digest(name string) (string, error) {
	return sha256File(filepath.Join(r.Dir, name))
}

// Digests is the digest of every file the release publishes, SHA256SUMS
// included.
func (r *Result) Digests() (map[string]string, error) {
	sums := make(map[string]string, len(r.Files))
	for _, name := range r.Files {
		sum, err := r.Digest(name)
		if err != nil {
			return nil, err
		}
		sums[name] = sum
	}
	return sums, nil
}

// Restamp writes the manifest and SHA256SUMS again after a change to the
// manifest or to one of the files, so neither can be left describing a release
// that no longer exists.
func (r *Result) Restamp() error {
	if err := r.Manifest.Write(filepath.Join(r.Dir, manifest.FileName)); err != nil {
		return fmt.Errorf("release: writing the manifest: %w", err)
	}
	sums := make([]build.Sum, 0, len(r.Files))
	for _, name := range r.Files {
		if name == build.ChecksumFile {
			continue
		}
		digest, err := r.Digest(name)
		if err != nil {
			return err
		}
		sums = append(sums, build.Sum{Name: name, SHA256: digest})
	}
	if _, err := build.WriteChecksums(r.Dir, sums); err != nil {
		return fmt.Errorf("release: writing %s: %w", build.ChecksumFile, err)
	}
	return nil
}
