// Package manifest is letsgo's own view of a release manifest.
//
// The types and most of the logic live in the public github.com/danielriddell21/letsgo/manifest
// package, exported so a plugin or another tool can read a manifest without a
// second set of types to keep in step. This package exists for the one place
// letsgo's own tools need to be stricter than that SDK promises to be:
// verify, diff and yank are rebuilding or comparing a specific release, so an
// unfamiliar schema is a reason to stop, not to decode what can be decoded.
package manifest

import (
	"fmt"
	"os"

	pub "github.com/danielriddell21/letsgo/manifest"
)

// Schema is the manifest format version this letsgo understands exactly.
const Schema = pub.Schema

// FileName is the manifest's published name.
const FileName = pub.FileName

type (
	Manifest      = pub.Manifest
	Features      = pub.Features
	Image         = pub.Image
	APIChange     = pub.APIChange
	Builder       = pub.Builder
	BuilderPlugin = pub.BuilderPlugin
	Source        = pub.Source
	Modules       = pub.Modules
	Module        = pub.Module
	Artifact      = pub.Artifact
	Binary        = pub.Binary
	Build         = pub.Build
	TapFile       = pub.TapFile
)

// Decode parses a manifest, rejecting schema versions it does not understand.
func Decode(data []byte) (*Manifest, error) {
	m, err := pub.Decode(data)
	if err != nil {
		return nil, err
	}
	if m.Schema != Schema {
		return nil, fmt.Errorf("manifest: schema %d is not supported (this letsgo understands %d)",
			m.Schema, Schema)
	}
	return m, nil
}

// Read loads a manifest from path.
func Read(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: reading %s: %w", path, err)
	}
	return Decode(data)
}

// SummariseModules reads go.sum and reports its digest, the number of distinct
// modules it pins, and their versions.
func SummariseModules(goSumPath string) (Modules, error) {
	return pub.SummariseModules(goSumPath)
}

// SortArtifacts orders artifacts by name so that two runs produce identical
// manifests regardless of the order work finished in.
func SortArtifacts(artifacts []Artifact) {
	pub.SortArtifacts(artifacts)
}

// BaseName strips the suffix the builder appends to every archive.
func BaseName(archive, version, goos, goarch string) string {
	return pub.BaseName(archive, version, goos, goarch)
}
