// Package manifest describes a release in a form a machine can act on.
//
// Every release publishes one letsgo.json alongside its artifacts. It records
// what was built, from what, by which toolchain, and with which digests —
// enough to rebuild the release and compare, without cloning the repository or
// scraping a release page.
//
// Four things fall out of that one file: verification, release-to-release
// diffing, an install script that checks what it downloads, and update
// checkers. None of them need code here; they need the data to exist.
package manifest

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Schema is the manifest format version. Consumers should refuse a manifest
// whose schema they do not recognise rather than guess at its shape.
const Schema = 1

// FileName is the manifest's published name.
const FileName = "letsgo.json"

// Manifest is a published release.
type Manifest struct {
	Schema  int    `json:"schema"`
	Project string `json:"project"`
	Version string `json:"version"`
	Tag     string `json:"tag,omitempty"`
	Commit  string `json:"commit"`

	// SourceDateEpoch is the commit timestamp every artifact was built
	// against. A rebuild that uses a different value cannot match.
	SourceDateEpoch int64 `json:"source_date_epoch"`

	Builder Builder           `json:"builder"`
	Source  *Source           `json:"source,omitempty"`
	Modules Modules           `json:"modules"`
	Gates   map[string]string `json:"gates,omitempty"`

	// APIChanges is the exported API delta against the previous release,
	// recorded when the gate ran. Kept so that comparing two releases needs
	// only their manifests: recomputing it would mean two checkouts and a
	// toolchain, to re-derive something already known at release time.
	APIChanges []APIChange `json:"api_changes,omitempty"`

	Artifacts []Artifact `json:"artifacts"`

	// Images are the container images the release published. Recording the
	// index digest is what lets verification ask whether the tag still points
	// at what was built, which is the one question a mutable tag cannot answer
	// on its own.
	Images []Image `json:"images,omitempty"`
}

// Image is one published container image.
type Image struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`

	// Tags are every tag the index was published under.
	Tags []string `json:"tags,omitempty"`

	Platforms []string `json:"platforms"`

	// Base is the base image, pinned to the digest that was actually used
	// rather than the tag that was written down.
	Base string `json:"base,omitempty"`
}

// APIChange is one difference in the exported API.
type APIChange struct {
	// Kind is "incompatible" or "compatible".
	Kind    string `json:"kind"`
	Package string `json:"package,omitempty"`
	Text    string `json:"text"`
}

// Builder records what produced the release. The toolchain is a build input:
// two Go versions can emit different code from identical source, so a
// verifier that ignores this will chase phantom differences.
type Builder struct {
	Tool string `json:"tool"`
	Go   string `json:"go"`
}

// Source is the deterministic source archive published with the release.
type Source struct {
	Archive string `json:"archive"`
	SHA256  string `json:"sha256"`
}

// Modules summarises the dependency graph. Recording go.sum's digest lets
// verification prove the dependencies matched, not merely the output.
type Modules struct {
	GoSumSHA256 string `json:"go_sum_sha256,omitempty"`
	Count       int    `json:"count"`

	// List names each module the build resolved. The digest above proves the
	// graph is unchanged; this says what changed when it is not, which is the
	// question anyone comparing two releases is actually asking.
	List []Module `json:"list,omitempty"`
}

// Module is one resolved dependency.
type Module struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Artifact is one published archive.
type Artifact struct {
	Name string `json:"name"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Size int64  `json:"size"`

	// Binary is the executable inside the archive. A module with several
	// commands produces one archive per command per target, and without this
	// the manifest cannot say which is which — which anything regenerating a
	// package definition from a published release has to know.
	Binary string `json:"binary,omitempty"`

	// BinarySize is the compiled binary's size before archiving. Archive size
	// moves with the compressor; this is the number that describes what a
	// user runs, and the one a size budget is written against.
	BinarySize int64 `json:"binary_size,omitempty"`

	// SHA256 is the archive's digest; BinarySHA256 is the digest of the
	// binary inside it. Keeping both means a failed verification says whether
	// the compiler or the packaging differed.
	SHA256       string `json:"sha256"`
	BinarySHA256 string `json:"binary_sha256"`

	Build Build `json:"build"`
}

// Build records exactly how an artifact was produced, so that reproducing it
// is a matter of replaying recorded inputs rather than guessing at them.
type Build struct {
	Flags   []string          `json:"flags"`
	LDFlags string            `json:"ldflags"`
	Env     map[string]string `json:"env"`
}

// Encode renders the manifest as indented JSON.
//
// The output is deterministic: fields follow struct order, encoding/json
// sorts map keys, and callers are expected to supply artifacts already sorted.
// A manifest that varied between runs would be one more thing verification had
// to forgive.
func (m *Manifest) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("manifest: encoding: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode parses a manifest, rejecting schema versions it does not understand.
func Decode(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: parsing: %w", err)
	}
	if m.Schema != Schema {
		return nil, fmt.Errorf("manifest: schema %d is not supported (this letsgo understands %d)",
			m.Schema, Schema)
	}
	return &m, nil
}

// Write writes the manifest to path.
func (m *Manifest) Write(path string) error {
	data, err := m.Encode()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("manifest: writing %s: %w", path, err)
	}
	return nil
}

// Read loads a manifest from path.
func Read(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: reading %s: %w", path, err)
	}
	return Decode(data)
}

// Artifact finds a published artifact by name.
func (m *Manifest) Artifact(name string) (Artifact, bool) {
	for _, a := range m.Artifacts {
		if a.Name == name {
			return a, true
		}
	}
	return Artifact{}, false
}

// SummariseModules reads go.sum and reports its digest, the number of distinct
// modules it pins, and their versions.
//
// A missing go.sum is not an error: a module with no dependencies has none,
// and that is a fact about the release rather than a problem with it.
func SummariseModules(goSumPath string) (Modules, error) {
	f, err := os.Open(goSumPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Modules{}, nil
		}
		return Modules{}, fmt.Errorf("manifest: reading %s: %w", goSumPath, err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(h, &buf), f); err != nil {
		return Modules{}, fmt.Errorf("manifest: reading %s: %w", goSumPath, err)
	}
	digest := hex.EncodeToString(h.Sum(nil))

	// go.sum lists each module twice, once for the archive and once for its
	// go.mod, so counting lines would double every dependency.
	seen := map[string]Module{}
	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		version := strings.TrimSuffix(fields[1], "/go.mod")
		seen[fields[0]+" "+version] = Module{Path: fields[0], Version: version}
	}
	if err := scanner.Err(); err != nil {
		return Modules{}, fmt.Errorf("manifest: reading %s: %w", goSumPath, err)
	}

	list := make([]Module, 0, len(seen))
	for _, m := range seen {
		list = append(list, m)
	}
	// Sorted so the manifest is identical between runs regardless of map
	// iteration order.
	sort.Slice(list, func(i, j int) bool {
		if list[i].Path != list[j].Path {
			return list[i].Path < list[j].Path
		}
		return list[i].Version < list[j].Version
	})

	return Modules{GoSumSHA256: digest, Count: len(list), List: list}, nil
}

// SortArtifacts orders artifacts by name so that two runs produce identical
// manifests regardless of the order work finished in.
func SortArtifacts(artifacts []Artifact) {
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
}
