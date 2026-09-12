package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"time"
)

// writeTarGz writes a deterministic gzip-compressed tar stream.
//
// Two layers each contribute their own sources of nondeterminism, and both are
// pinned here rather than left to the standard library's defaults.
func writeTarGz(w io.Writer, entries []Entry, modTime time.Time) error {
	zw, err := NewGzipWriter(w)
	if err != nil {
		return err
	}
	if err := writeTar(zw, entries, modTime); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("archive: closing gzip: %w", err)
	}
	return nil
}

// NewGzipWriter returns a gzip writer whose output depends only on its input.
//
// Exported because an image layer is compressed outside this package and must
// be compressed the same way; two definitions of "deterministic gzip" would be
// one too many.
func NewGzipWriter(w io.Writer) (*gzip.Writer, error) {
	// The compression level is part of the output. Pinning it means a future
	// change to the default cannot silently invalidate published checksums.
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("archive: gzip writer: %w", err)
	}

	// gzip headers carry an original filename, a modification time and a byte
	// identifying the source operating system. Left alone, the last two vary
	// with the clock and the builder's platform, so a Linux and a macOS runner
	// producing identical tar streams would still emit different .tar.gz files.
	zw.Name = ""
	zw.Comment = ""
	zw.Extra = nil
	zw.ModTime = time.Time{} // zero: written as MTIME 0, meaning "no timestamp"
	zw.OS = 255              // unknown, rather than the building platform

	return zw, nil
}

// writeTar writes a deterministic uncompressed tar stream.
func writeTar(w io.Writer, entries []Entry, modTime time.Time) error {
	tw := tar.NewWriter(w)

	for _, e := range entries {
		typeflag, name, size := byte(tar.TypeReg), e.Path, e.Size
		if e.Dir {
			// A trailing slash is what every extractor keys on, and a
			// directory entry carrying a size would be malformed.
			typeflag, name, size = tar.TypeDir, e.Path+"/", 0
		}

		hdr := &tar.Header{
			Typeflag: typeflag,
			Name:     name,
			Size:     size,
			Mode:     int64(e.mode()),
			ModTime:  modTime,

			// Ownership is zeroed rather than inherited. A tar built as uid
			// 1000 on a laptop and as uid 0 in a container are otherwise
			// different files with identical contents.
			Uid:   0,
			Gid:   0,
			Uname: "",
			Gname: "",

			// AccessTime and ChangeTime are deliberately left at their zero
			// value. Setting either would push the header into PAX format and
			// add records that carry no useful information about a release.

			// USTAR is the most constrained format that fits our needs, which
			// is exactly why it is chosen: it has nowhere to put the extra
			// metadata that the richer formats would record.
			Format: tar.FormatUSTAR,
		}

		if err := tw.WriteHeader(hdr); err != nil {
			// The most likely cause is a path too long for a USTAR header.
			// Say so, rather than letting the caller guess.
			return fmt.Errorf("archive: tar header for %q (paths must fit USTAR limits): %w", e.Path, err)
		}
		if e.Dir {
			continue
		}
		if err := copyExactly(tw, e); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("archive: closing tar: %w", err)
	}
	return nil
}
