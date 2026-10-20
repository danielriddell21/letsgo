package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Record is what the installer leaves beside each binary it installs, and
// what selfupdate rewrites when it replaces one: how the binary got there,
// from which release, and the digest it had when it was checked. It lets a
// later command say whether the binary on disk is still that one.
type Record struct {
	Schema int `json:"schema"`

	// Via is what wrote the binary: "install.sh" or "selfupdate".
	Via string `json:"via"`

	Project        string `json:"project"`
	Version        string `json:"version"`
	Tag            string `json:"tag"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	Archive        string `json:"archive"`
	ArchiveSHA256  string `json:"archive_sha256"`
	Binary         string `json:"binary"`
	BinarySHA256   string `json:"binary_sha256"`
}

// RecordSchema is the Record format this letsgo writes.
const RecordSchema = 1

// RecordPath is where the record for the binary at path lives: a dotfile
// beside it, so binaries sharing a directory such as /usr/local/bin each
// have their own.
func RecordPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".install.json")
}

// ReadRecord reads the record for the binary at path. No record is not an
// error: most binaries were not installed by install.sh, and it returns nil.
func ReadRecord(path string) (*Record, error) {
	data, err := os.ReadFile(RecordPath(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("install: reading the install record: %w", err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("install: %s is not an install record: %w", RecordPath(path), err)
	}
	return &r, nil
}

// WriteRecord writes r as the record for the binary at path.
func WriteRecord(path string, r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("install: encoding the install record: %w", err)
	}
	if err := os.WriteFile(RecordPath(path), append(data, '\n'), 0o644); err != nil { //nolint:gosec // a record is not a secret
		return fmt.Errorf("install: writing the install record: %w", err)
	}
	return nil
}
