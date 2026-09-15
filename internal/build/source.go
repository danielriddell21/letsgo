package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/discover"
)

// SourceOptions describes a source archive to produce.
type SourceOptions struct {
	ModuleDir string
	Name      string
	Version   string
	ModTime   time.Time
	WorkDir   string
}

// Source is a produced source archive.
type Source struct {
	Name   string
	SHA256 string
	Size   int64
}

// WriteSource produces a deterministic archive of the repository's tracked
// files.
//
// Forges generate source tarballs on demand, and those tarballs are not
// guaranteed to be byte-stable over time: a change in how they are produced
// has previously invalidated checksums that downstream packagers had already
// published. Anything pinning the digest of an auto-generated tarball — a
// Homebrew formula most commonly — inherits that risk.
//
// Publishing our own costs one extra asset and removes the dependency
// entirely. A checksum we generate is a checksum we control.
func WriteSource(ctx context.Context, o SourceOptions) (Source, error) {
	if o.WorkDir == "" || o.ModuleDir == "" {
		return Source{}, fmt.Errorf("build: WorkDir and ModuleDir are required")
	}

	files, err := discover.TrackedFiles(ctx, o.ModuleDir)
	if err != nil {
		return Source{}, err
	}
	if len(files) == 0 {
		return Source{}, fmt.Errorf("build: no tracked files to archive")
	}

	// A top-level directory means extracting the archive cannot scatter files
	// across the user's current directory, which is the convention every forge
	// follows for the same reason.
	prefix := fmt.Sprintf("%s-%s", o.Name, o.Version)

	entries := make([]archive.Entry, 0, len(files))
	for _, name := range files {
		onDisk := filepath.Join(o.ModuleDir, filepath.FromSlash(name))

		// A tracked file can be absent from the worktree — a sparse checkout,
		// or a submodule directory. Skipping is wrong: the archive would
		// silently omit source. Say so instead.
		if _, err := os.Lstat(onDisk); err != nil {
			return Source{}, fmt.Errorf("build: tracked file %s is missing from the worktree: %w", name, err)
		}

		entry, err := archive.FromFile(prefix+"/"+name, onDisk)
		if err != nil {
			return Source{}, err
		}
		entries = append(entries, entry)
	}

	name := fmt.Sprintf("%s_%s_source%s", o.Name, o.Version, archive.FormatTarGz.Ext())
	path := filepath.Join(o.WorkDir, name)

	if err := os.MkdirAll(o.WorkDir, 0o750); err != nil {
		return Source{}, fmt.Errorf("build: %w", err)
	}
	if err := writeArchive(path, archive.FormatTarGz, entries, o.ModTime); err != nil {
		return Source{}, err
	}

	sum, err := sha256File(path)
	if err != nil {
		return Source{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Source{}, fmt.Errorf("build: %w", err)
	}

	return Source{Name: name, SHA256: sum, Size: info.Size()}, nil
}

// DocumentationFiles are the repository-root files included in each archive
// when they exist.
//
// Exported because verification has to reach the same answer as the release
// did from the same tree; two copies of this list would be two chances to
// disagree about what a published archive contains.
// Both spellings of "licence" are listed: the British one is not rare, and
// omitting it silently ships an archive with no licence in it. The cost is one
// os.Stat.
var DocumentationFiles = []string{
	"README.md", "README",
	"LICENSE", "LICENSE.md", "LICENCE", "LICENCE.md",
	"CHANGELOG.md",
}

// FindDocumentation returns the documentation files present in dir, in a
// fixed order.
func FindDocumentation(dir string) []string {
	var found []string
	for _, name := range DocumentationFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			found = append(found, name)
		}
	}
	return found
}
