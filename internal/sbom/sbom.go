// Package sbom describes a release's dependencies in a form other tools read.
//
// CycloneDX 1.6, written by hand: the schema is small, the module graph is
// already known exactly, and the alternative is a dependency for a tool whose
// whole job is to say which dependencies a release has.
//
// The output is deterministic, which generated SBOMs usually are not. The
// conventional ones stamp a wall-clock timestamp and a random serial number
// into every run, so two SBOMs for the same commit differ and neither can be
// checked against the other. Here the timestamp is the commit time and the
// serial number is derived from the module, version and commit — so the file
// is reproducible for the same reason the binaries are, and `letsgo verify`
// covers it through SHA256SUMS.
package sbom

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/manifest"
)

// FileName is the SBOM's published name.
const FileName = "sbom.cdx.json"

// specVersion is the CycloneDX version emitted. Pinned rather than tracking
// the latest: the format is an output, and an output that changes shape on its
// own breaks whoever was reading it.
const specVersion = "1.6"

// Options describe the release to document.
type Options struct {
	Project    string
	ModulePath string
	Version    string
	Commit     string

	// Repo is the "owner/name" the release was published from, for the
	// external references a consumer follows back to the source.
	Repo string

	// Created must be the commit time. Taking it from the clock is what makes
	// every other SBOM generator's output unreproducible.
	Created time.Time

	// Tool is the letsgo version that produced the release.
	Tool string

	GoVersion string
	Modules   []manifest.Module
	Artifacts []manifest.Artifact
}

// Generate renders the SBOM.
func Generate(o Options) ([]byte, error) {
	if o.ModulePath == "" || o.Version == "" {
		return nil, fmt.Errorf("sbom: module path and version are required")
	}

	doc := document{
		BOMFormat:    "CycloneDX",
		SpecVersion:  specVersion,
		SerialNumber: serial(o),
		Version:      1,
		Metadata: metadata{
			Timestamp: o.Created.UTC().Format(time.RFC3339),
			Tools:     tools{Components: []component{letsgoComponent(o.Tool)}},
			Component: rootComponent(o),
		},
	}

	root := purl(o.ModulePath, "v"+strings.TrimPrefix(o.Version, "v"))
	depends := make([]string, 0, len(o.Modules)+1)

	// The toolchain is a dependency of the binary in every sense that matters:
	// it contributes the runtime, the standard library and the code generator.
	// Leaving it out is how an SBOM ends up unable to answer a question about
	// a standard-library advisory.
	if o.GoVersion != "" {
		toolchain := component{
			Type:    "application",
			Name:    "go",
			Version: strings.TrimPrefix(o.GoVersion, "go"),
			PURL:    purl("golang.org/toolchain", o.GoVersion),
			BOMRef:  purl("golang.org/toolchain", o.GoVersion),
			Scope:   "required",
		}
		doc.Components = append(doc.Components, toolchain)
		depends = append(depends, toolchain.BOMRef)
	}

	for _, m := range o.Modules {
		ref := purl(m.Path, m.Version)
		doc.Components = append(doc.Components, component{
			Type:    "library",
			Name:    m.Path,
			Version: m.Version,
			PURL:    ref,
			BOMRef:  ref,
			Scope:   "required",
		})
		depends = append(depends, ref)
	}

	// The published files, with the digests the release recorded. A consumer
	// holding an archive can then tell whether this SBOM describes it.
	for _, a := range o.Artifacts {
		doc.Components = append(doc.Components, component{
			Type:   "file",
			Name:   a.Name,
			BOMRef: "file:" + a.Name,
			Hashes: []hash{{Algorithm: "SHA-256", Content: a.SHA256}},
		})
	}

	sort.Slice(doc.Components, func(i, j int) bool {
		if doc.Components[i].Type != doc.Components[j].Type {
			return doc.Components[i].Type < doc.Components[j].Type
		}
		return doc.Components[i].BOMRef < doc.Components[j].BOMRef
	})
	sort.Strings(depends)

	doc.Dependencies = []dependency{{Ref: root, DependsOn: depends}}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("sbom: encoding: %w", err)
	}
	return buf.Bytes(), nil
}

func rootComponent(o Options) component {
	c := component{
		Type:    "application",
		Name:    o.Project,
		Version: o.Version,
		PURL:    purl(o.ModulePath, "v"+strings.TrimPrefix(o.Version, "v")),
		BOMRef:  purl(o.ModulePath, "v"+strings.TrimPrefix(o.Version, "v")),
	}
	if o.Repo != "" {
		c.ExternalReferences = []externalReference{
			{Type: "vcs", URL: "https://github.com/" + o.Repo},
			{Type: "distribution", URL: "https://github.com/" + o.Repo + "/releases/tag/v" +
				strings.TrimPrefix(o.Version, "v")},
		}
	}
	if o.Commit != "" {
		c.Properties = []property{{Name: "letsgo:commit", Value: o.Commit}}
	}
	return c
}

func letsgoComponent(version string) component {
	return component{
		Type:    "application",
		Name:    "letsgo",
		Version: version,
		BOMRef:  "letsgo",
		ExternalReferences: []externalReference{
			{Type: "website", URL: "https://github.com/danielriddell21/letsgo"},
		},
	}
}

// purl renders a Package URL for a Go module, which is the identifier every
// consumer of this file keys on.
func purl(path, version string) string {
	// The namespace is lowercased by the purl specification for golang, and
	// the version is not.
	return "pkg:golang/" + strings.ToLower(path) + "@" + version
}

// serial derives the document's serial number from the release rather than
// from a random source, so two runs over the same commit produce one file
// rather than two that differ only in an identifier nobody reads.
func serial(o Options) string {
	sum := sha256.Sum256([]byte(o.ModulePath + "\x00" + o.Version + "\x00" + o.Commit))
	h := hex.EncodeToString(sum[:])

	// Shaped as a UUID: consumers validate the field, and the bits that carry
	// meaning are the ones derived above.
	return fmt.Sprintf("urn:uuid:%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}
