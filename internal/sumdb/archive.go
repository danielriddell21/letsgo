package sumdb

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxArchiveFile bounds one entry read from the source archive, matching
// verify's own bound on the same kind of input (internal/verify/rebuild.go's
// maxSourceFile).
const maxArchiveFile = 256 << 20

// readArchive reads a tar.gz source archive (as internal/build.WriteSource
// produces) into memory, keyed by each file's path relative to the
// archive's own top-level directory — the module zip carries a different
// prefix of its own, so only the part after it is comparable.
func readArchive(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sumdb: %w", err)
	}
	defer func() { _ = f.Close() }()

	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("sumdb: reading %s: %w", path, err)
	}
	tr := tar.NewReader(zr)

	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("sumdb: reading %s: %w", path, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		name := hdr.Name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}

		data, err := io.ReadAll(io.LimitReader(tr, maxArchiveFile))
		if err != nil {
			return nil, fmt.Errorf("sumdb: reading %s from %s: %w", hdr.Name, path, err)
		}
		files[name] = data
	}
}
