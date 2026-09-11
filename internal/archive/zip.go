package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"time"
)

// writeZip writes a deterministic zip archive.
//
// Zip is used for Windows targets, where a .tar.gz is inconvenient to open
// without extra tooling.
func writeZip(w io.Writer, entries []Entry, modTime time.Time) error {
	zw := zip.NewWriter(w)

	for _, e := range entries {
		hdr := &zip.FileHeader{
			Name:   e.Path,
			Method: zip.Deflate,

			// Modified must be in UTC. The zip writer derives both an MS-DOS
			// timestamp and an extended-timestamp extra field from this value,
			// and a non-UTC location leaks the builder's timezone offset into
			// the latter. Write normalises this before we are called; setting
			// it here as well would be redundant, so we rely on that contract.
			Modified: modTime,
		}
		hdr.SetMode(e.mode())

		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("archive: zip header for %q: %w", e.Path, err)
		}
		if err := copyExactly(fw, e); err != nil {
			return err
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("archive: closing zip: %w", err)
	}
	return nil
}
