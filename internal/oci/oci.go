// Package oci builds container images without a daemon or a build system.
//
// An image around a static Go binary is not a build problem. The binary
// already exists; what remains is a tar layer, a JSON config, a JSON manifest
// and four HTTP requests. Wrapping `docker buildx` to produce that would import
// a build system, a daemon and a cache to do work this package does in a few
// hundred lines — and would give up reproducibility in the process, because
// the answer would then depend on the builder's Docker version.
//
// Everything here is a pure function of its inputs. Two machines given the
// same binary and the same commit timestamp produce byte-identical layers,
// configs and manifests, and therefore the same image digest.
package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Media types from the OCI image specification.
const (
	MediaTypeManifest   = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeIndex      = "application/vnd.oci.image.index.v1+json"
	MediaTypeConfig     = "application/vnd.oci.image.config.v1+json"
	MediaTypeLayerGzip  = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeDockerList = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeDockerMani = "application/vnd.docker.distribution.manifest.v2+json"
)

// Digest is a content digest in registry form, "sha256:<hex>".
type Digest string

// DigestOf returns the digest of a blob.
func DigestOf(blob []byte) Digest {
	sum := sha256.Sum256(blob)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

// Hex returns the digest without its algorithm prefix.
func (d Digest) Hex() string {
	_, hex, _ := strings.Cut(string(d), ":")
	return hex
}

// Short returns an abbreviated digest for reporting.
func (d Digest) Short() string {
	hex := d.Hex()
	if len(hex) > 12 {
		return hex[:12]
	}
	return hex
}

// Descriptor points at a blob.
type Descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    Digest    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *Platform `json:"platform,omitempty"`

	// Annotations carry the provenance a registry UI shows. Only set on
	// manifests, never on layers, where they would change the layer digest
	// for no benefit.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Platform is an image's target.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

func (p Platform) String() string {
	if p.Variant == "" {
		return p.OS + "/" + p.Architecture
	}
	return p.OS + "/" + p.Architecture + "/" + p.Variant
}

// Manifest is an OCI image manifest: one config, some layers, one platform.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Index is a multi-platform image: one manifest per platform.
type Index struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Manifests     []Descriptor      `json:"manifests"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Config is an image configuration blob.
type Config struct {
	Created      string       `json:"created,omitempty"`
	Architecture string       `json:"architecture"`
	OS           string       `json:"os"`
	Variant      string       `json:"variant,omitempty"`
	Config       RunConfig    `json:"config"`
	RootFS       RootFS       `json:"rootfs"`
	History      []HistoryRec `json:"history,omitempty"`
}

// RunConfig is what the runtime does with the image.
type RunConfig struct {
	Entrypoint []string `json:"Entrypoint,omitempty"`
	Cmd        []string `json:"Cmd,omitempty"`
	Env        []string `json:"Env,omitempty"`
	WorkingDir string   `json:"WorkingDir,omitempty"`
	User       string   `json:"User,omitempty"`

	// ExposedPorts is a set, which is how the image spec models it: the keys
	// are "port/proto" and the values carry nothing. It opens no port — it is
	// metadata a registry UI displays and `docker run -P` reads.
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`

	Labels map[string]string `json:"Labels,omitempty"`
}

// RootFS lists the uncompressed digests of the stacked layers.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []Digest `json:"diff_ids"`
}

// HistoryRec is one entry in the image's build history.
type HistoryRec struct {
	Created    string `json:"created,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	EmptyLayer bool   `json:"empty_layer,omitempty"`
}

// encode renders a JSON blob the way a registry will store it.
//
// Compact and with no trailing newline, because the bytes are the identity: a
// blob's digest is computed over exactly what is uploaded, so any formatting
// choice here is permanent.
func encode(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("oci: encoding: %w", err)
	}
	return data, nil
}

// decode parses a JSON blob this package produced.
func decode(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("oci: parsing: %w", err)
	}
	return nil
}
