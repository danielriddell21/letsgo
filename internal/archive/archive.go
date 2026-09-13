// Package archive writes byte-for-byte reproducible release archives.
//
// Every field that could vary between two machines or two runs is pinned:
// entries are sorted, ownership is zeroed, modes are normalised to one of two
// values, and all timestamps come from a caller-supplied instant — in practice
// the commit time — rather than from the clock.
//
// The zero values in this package are load bearing. Where a field is set to
// its zero value explicitly, that is deliberate, and the comment says why.
package archive

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// Format identifies an archive container.
type Format string

const (
	FormatTarGz Format = "tar.gz"
	FormatZip   Format = "zip"

	// FormatTar is an uncompressed tar stream. Release archives are never
	// published this way; it exists because an OCI layer is digested twice,
	// once compressed and once not, and both digests have to come from the
	// same deterministic writer.
	FormatTar Format = "tar"
)

// Ext returns the file extension for the format, including the leading dot.
func (f Format) Ext() string {
	switch f {
	case FormatTarGz:
		return ".tar.gz"
	case FormatZip:
		return ".zip"
	case FormatTar:
		return ".tar"
	default:
		return ""
	}
}

// Modes are normalised to exactly these two values. Preserving the mode bits
// from disk would leak the builder's umask into the archive, which is one of
// the more surprising ways an otherwise deterministic build stops being
// reproducible across machines.
const (
	modeExecutable fs.FileMode = 0o755
	modeRegular    fs.FileMode = 0o644
)

// Entry is one file to place in an archive.
type Entry struct {
	// Path is the slash-separated path inside the archive.
	Path string

	// Executable selects the normalised mode: 0755 when true, 0644 otherwise.
	Executable bool

	// Dir makes the entry a directory. A release archive has none; an image
	// layer needs them, because the path a binary lives at may not exist in
	// the base it is stacked on — and on `scratch` nothing exists at all.
	Dir bool

	// Size is the number of bytes Open yields.
	Size int64

	// Open returns the entry's contents. It may be called more than once.
	Open func() (io.ReadCloser, error)
}

func (e Entry) mode() fs.FileMode {
	if e.Executable || e.Dir {
		return modeExecutable
	}
	return modeRegular
}

// FromFile builds an Entry reading from a file on disk. The file's mode bits
// are consulted only to decide whether the entry is executable; they are not
// otherwise carried into the archive.
func FromFile(archivePath, diskPath string) (Entry, error) {
	info, err := os.Stat(diskPath)
	if err != nil {
		return Entry{}, err
	}
	if info.IsDir() {
		return Entry{}, fmt.Errorf("archive: %s is a directory", diskPath)
	}
	return Entry{
		Path:       archivePath,
		Executable: info.Mode().Perm()&0o111 != 0,
		Size:       info.Size(),
		Open:       func() (io.ReadCloser, error) { return os.Open(diskPath) },
	}, nil
}

// FromBytes builds an Entry from an in-memory buffer.
func FromBytes(archivePath string, executable bool, data []byte) Entry {
	return Entry{
		Path:       archivePath,
		Executable: executable,
		Size:       int64(len(data)),
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(data))), nil
		},
	}
}

// Write writes entries to w in the given format.
//
// modTime is applied to every entry. Callers pass the commit time; passing
// time.Now would make the output non-reproducible and is never correct here.
func Write(w io.Writer, f Format, entries []Entry, modTime time.Time) error {
	normalised, err := normalise(entries)
	if err != nil {
		return err
	}
	// Sub-second precision cannot be represented in a USTAR header or a DOS
	// timestamp, and carrying it would force a format upgrade that varies with
	// the input. Truncate once, here, so both writers agree.
	modTime = modTime.UTC().Truncate(time.Second)

	switch f {
	case FormatTarGz:
		return writeTarGz(w, normalised, modTime)
	case FormatTar:
		return writeTar(w, normalised, modTime)
	case FormatZip:
		return writeZip(w, normalised, modTime)
	default:
		return fmt.Errorf("archive: unknown format %q", f)
	}
}

// normalise validates entry paths and returns them sorted. Sorting is what
// makes the output independent of the order the caller happened to discover
// files in, which on a filesystem walk is not guaranteed to be stable.
func normalise(entries []Entry) ([]Entry, error) {
	out := slices.Clone(entries)
	seen := make(map[string]bool, len(out))

	for i, e := range out {
		if e.Path == "" {
			return nil, fmt.Errorf("archive: entry %d has an empty path", i)
		}
		if strings.ContainsRune(e.Path, '\\') {
			return nil, fmt.Errorf("archive: entry %q must use forward slashes", e.Path)
		}
		if path.IsAbs(e.Path) {
			return nil, fmt.Errorf("archive: entry %q must be relative", e.Path)
		}
		if cleaned := path.Clean(e.Path); cleaned != e.Path {
			return nil, fmt.Errorf("archive: entry %q is not clean (want %q)", e.Path, cleaned)
		}
		if e.Path == ".." || strings.HasPrefix(e.Path, "../") {
			return nil, fmt.Errorf("archive: entry %q escapes the archive root", e.Path)
		}
		if seen[e.Path] {
			return nil, fmt.Errorf("archive: duplicate entry %q", e.Path)
		}
		seen[e.Path] = true

		if e.Dir {
			if e.Open != nil || e.Size != 0 {
				return nil, fmt.Errorf("archive: directory entry %q has content", e.Path)
			}
			continue
		}
		if e.Open == nil {
			return nil, fmt.Errorf("archive: entry %q has no Open function", e.Path)
		}
	}

	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// copyExactly copies the entry's contents and verifies the byte count matches
// the declared size. A mismatch means the file changed underneath us, which
// would silently corrupt a tar stream rather than fail it.
func copyExactly(w io.Writer, e Entry) error {
	rc, err := e.Open()
	if err != nil {
		return fmt.Errorf("archive: opening %q: %w", e.Path, err)
	}
	defer rc.Close()

	n, err := io.Copy(w, rc)
	if err != nil {
		return fmt.Errorf("archive: writing %q: %w", e.Path, err)
	}
	if n != e.Size {
		return fmt.Errorf("archive: %q declared %d bytes but yielded %d", e.Path, e.Size, n)
	}
	return nil
}
