package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ChecksumFile is the conventional name for the digest manifest shipped
// alongside release artifacts.
const ChecksumFile = "SHA256SUMS"

// Sum pairs a published filename with its digest.
type Sum struct {
	Name   string
	SHA256 string
}

// SumsFor collects the digests of built artifacts.
func SumsFor(artifacts []Artifact) []Sum {
	sums := make([]Sum, 0, len(artifacts))
	for _, a := range artifacts {
		sums = append(sums, Sum{Name: a.Archive, SHA256: a.ArchiveSHA256})
	}
	return sums
}

// WriteChecksums writes a SHA256SUMS file in the format sha256sum -c expects.
//
// Entries are sorted here rather than trusted to arrive in order, so the file
// is deterministic for the same reason the archives are: nothing about its
// content depends on the order work happened to complete in.
func WriteChecksums(dir string, sums []Sum) (string, error) {
	ordered := make([]Sum, len(sums))
	copy(ordered, sums)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })

	var b strings.Builder
	for _, s := range ordered {
		// Two spaces, then the name: the format coreutils reads back.
		fmt.Fprintf(&b, "%s  %s\n", s.SHA256, s.Name)
	}

	path := filepath.Join(dir, ChecksumFile)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("build: writing %s: %w", ChecksumFile, err)
	}
	return path, nil
}
