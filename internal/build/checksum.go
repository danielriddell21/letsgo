package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ChecksumFile is the conventional name for the digest manifest shipped
// alongside release artifacts.
const ChecksumFile = "SHA256SUMS"

// WriteChecksums writes a SHA256SUMS file in the format sha256sum -c expects.
//
// Artifacts arrive sorted, so the file is deterministic for the same reason
// the archives are: nothing about its content depends on the order work
// happened to complete in.
func WriteChecksums(dir string, artifacts []Artifact) (string, error) {
	var b strings.Builder
	for _, a := range artifacts {
		// Two spaces, then the name: the format coreutils reads back.
		fmt.Fprintf(&b, "%s  %s\n", a.ArchiveSHA256, a.Archive)
	}

	path := filepath.Join(dir, ChecksumFile)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("build: writing %s: %w", ChecksumFile, err)
	}
	return path, nil
}
