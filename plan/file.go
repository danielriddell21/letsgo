package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// FileSchema is the plan file format this package reads and writes.
const FileSchema = 1

// FileKindRelease marks a plan file that publishes a release.
const FileKindRelease = "release"

// File is a saved plan: what was agreed, to be applied later.
//
// It holds intent and fingerprints, never artifact bytes. The predicted
// manifest says what a rebuild must reproduce; the actions say what the forge
// will be asked to do. Applying it rebuilds and compares, so the file is
// trusted for nothing it cannot be checked against.
type File struct {
	Schema        int    `json:"schema"`
	LetsgoVersion string `json:"letsgo_version"`
	CreatedAt     string `json:"created_at"`
	Kind          string `json:"kind"`
	Repo          string `json:"repo"`
	Tag           string `json:"tag"`
	Commit        string `json:"commit"`

	// ManifestSHA256 is the digest of the manifest a rebuild must reproduce
	// byte for byte; Manifest is that manifest, for reading and for naming
	// what differs when it is not reproduced.
	ManifestSHA256 string          `json:"manifest_sha256"`
	Manifest       json.RawMessage `json:"manifest"`

	Actions []Action `json:"actions"`
}

// Encode is the file as written to disk.
func (f *File) Encode() ([]byte, error) {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return append(data, '\n'), nil
}

// Digest identifies the plan: the sha256 of its canonical, compact JSON, so
// that re-indenting the file does not change it.
func (f *File) Digest() (string, error) {
	data, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("plan: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Changes reports whether applying the plan would do anything.
func (f *File) Changes() bool {
	return HasChanges(f.Actions)
}

// HasChanges reports whether any action adds, changes or removes something.
func HasChanges(actions []Action) bool {
	add, change, remove := Counts(actions)
	return add+change+remove > 0
}

// Decode parses a plan file, refusing a schema or kind it does not apply.
func Decode(data []byte) (*File, error) {
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if f.Schema != FileSchema {
		return nil, fmt.Errorf("plan: schema %d is not supported (this letsgo applies %d)", f.Schema, FileSchema)
	}
	if f.Kind != FileKindRelease {
		return nil, fmt.Errorf("plan: cannot apply a %q plan", f.Kind)
	}
	if f.Tag == "" || f.Commit == "" || f.ManifestSHA256 == "" {
		return nil, errors.New("plan: the file is missing its tag, commit or manifest digest")
	}
	return &f, nil
}

// Write saves the plan to path.
func (f *File) Write(path string) error {
	data, err := f.Encode()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a plan is meant to be read and shared
		return fmt.Errorf("plan: %w", err)
	}
	return nil
}

// Read loads a plan file from path.
func Read(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return Decode(data)
}
