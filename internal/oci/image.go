package oci

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/archive"
)

// BinDir is where the binary is placed inside the image. It is on the default
// PATH of every base worth stacking on, and absolute, so the entrypoint works
// on `scratch` too.
const BinDir = "/usr/local/bin"

// Blob is a piece of content to upload, kept in memory because an image built
// from one binary is small and the alternative is a temporary directory whose
// cleanup is one more thing to get wrong.
type Blob struct {
	Digest    Digest
	MediaType string
	Content   []byte
}

// Size is the blob's length.
func (b Blob) Size() int64 { return int64(len(b.Content)) }

// ImageOptions describe one platform's image.
type ImageOptions struct {
	// Binary is the path on disk of the executable to ship, and Name is what
	// it is called inside the image.
	Binary string
	Name   string

	Platform Platform

	// Created is the image's timestamp. It must come from the commit, never
	// the clock: an image stamped with the build time cannot reproduce.
	Created time.Time

	// Base is the image to stack on. The zero value means `scratch`.
	Base *Base

	// Annotations are attached to the manifest and mirrored into the config's
	// labels, which is where most registry UIs read them from.
	Annotations map[string]string
}

// Base is a resolved base image: its config and layers, already pinned to
// digests. Nothing here is fetched by this package — resolving a reference to
// these bytes is the registry client's job.
type Base struct {
	Reference string

	// Digest names this platform's manifest, which is what stacks. IndexDigest
	// names what the reference itself resolved to — the index, where there is
	// one — which is the platform-independent pin worth recording.
	Digest      Digest
	IndexDigest Digest

	// Config is the base's parsed configuration, whose rootfs and run settings
	// the new image inherits.
	Config Config

	// Layers are the base's layer descriptors, stacked below ours in order.
	Layers []Descriptor
}

// Image is one platform's assembled image.
type Image struct {
	Platform Platform

	Layer    Blob
	ConfigJS Blob
	Manifest Blob

	// Descriptor points at Manifest, ready to go into an index.
	Descriptor Descriptor
}

// BuildImage assembles one platform's image from a binary on disk.
func BuildImage(o ImageOptions) (*Image, error) {
	if o.Binary == "" || o.Name == "" {
		return nil, fmt.Errorf("oci: a binary and a name are required")
	}
	if o.Platform.OS == "" || o.Platform.Architecture == "" {
		return nil, fmt.Errorf("oci: %s has no platform", o.Name)
	}

	layer, diffID, err := buildLayer(o)
	if err != nil {
		return nil, err
	}

	config := Config{
		Created:      o.Created.UTC().Format(time.RFC3339),
		Architecture: o.Platform.Architecture,
		OS:           o.Platform.OS,
		Variant:      o.Platform.Variant,
		Config: RunConfig{
			Entrypoint: []string{path.Join(BinDir, o.Name)},
			Labels:     o.Annotations,
		},
		RootFS: RootFS{Type: "layers"},
	}

	var layers []Descriptor
	if o.Base != nil {
		// The base's settings are inherited and then overridden, rather than
		// replaced wholesale: a base that sets Env or User is usually setting
		// it for a reason, and the entrypoint is the only thing we know better.
		config.Config.Env = o.Base.Config.Config.Env
		config.Config.User = o.Base.Config.Config.User
		config.Config.WorkingDir = o.Base.Config.Config.WorkingDir
		config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, o.Base.Config.RootFS.DiffIDs...)
		config.History = append(config.History, o.Base.Config.History...)
		layers = append(layers, o.Base.Layers...)
	}

	config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, diffID)
	config.History = append(config.History, HistoryRec{
		Created:   config.Created,
		CreatedBy: "letsgo",
	})

	configJSON, err := encode(config)
	if err != nil {
		return nil, err
	}
	configBlob := Blob{Digest: DigestOf(configJSON), MediaType: MediaTypeConfig, Content: configJSON}

	layers = append(layers, Descriptor{
		MediaType: MediaTypeLayerGzip, Digest: layer.Digest, Size: layer.Size(),
	})

	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeManifest,
		Config: Descriptor{
			MediaType: MediaTypeConfig, Digest: configBlob.Digest, Size: configBlob.Size(),
		},
		Layers:      layers,
		Annotations: o.Annotations,
	}

	manifestJSON, err := encode(manifest)
	if err != nil {
		return nil, err
	}
	manifestBlob := Blob{
		Digest: DigestOf(manifestJSON), MediaType: MediaTypeManifest, Content: manifestJSON,
	}

	platform := o.Platform
	return &Image{
		Platform: o.Platform,
		Layer:    layer,
		ConfigJS: configBlob,
		Manifest: manifestBlob,
		Descriptor: Descriptor{
			MediaType: MediaTypeManifest,
			Digest:    manifestBlob.Digest,
			Size:      manifestBlob.Size(),
			Platform:  &platform,
		},
	}, nil
}

// buildLayer produces the compressed layer and the digest of its uncompressed
// form, which is the diff_id the config records.
func buildLayer(o ImageOptions) (Blob, Digest, error) {
	info, err := os.Stat(o.Binary)
	if err != nil {
		return Blob{}, "", fmt.Errorf("oci: %w", err)
	}

	entries := directoriesFor(BinDir)
	entries = append(entries, archive.Entry{
		Path:       path.Join(BinDir, o.Name)[1:],
		Executable: true,
		Size:       info.Size(),
		Open:       func() (io.ReadCloser, error) { return os.Open(o.Binary) },
	})

	// The layer is digested twice: uncompressed for the config's diff_id, and
	// compressed for the descriptor the registry stores. Both come from the
	// same writer so the two cannot disagree.
	var raw bytes.Buffer
	if err := archive.Write(&raw, archive.FormatTar, entries, o.Created); err != nil {
		return Blob{}, "", err
	}
	diffID := DigestOf(raw.Bytes())

	var compressed bytes.Buffer
	zw, err := archive.NewGzipWriter(&compressed)
	if err != nil {
		return Blob{}, "", err
	}
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return Blob{}, "", fmt.Errorf("oci: compressing layer: %w", err)
	}
	if err := zw.Close(); err != nil {
		return Blob{}, "", fmt.Errorf("oci: compressing layer: %w", err)
	}

	content := compressed.Bytes()
	return Blob{Digest: DigestOf(content), MediaType: MediaTypeLayerGzip, Content: content}, diffID, nil
}

// directoriesFor returns the parent directories of an absolute path, outermost
// first. A layer that names a file under a directory the base does not have —
// and `scratch` has none — needs them spelled out.
func directoriesFor(dir string) []archive.Entry {
	parts := splitPath(dir)
	entries := make([]archive.Entry, 0, len(parts))

	var built string
	for _, part := range parts {
		built = path.Join(built, part)
		entries = append(entries, archive.Entry{Path: built, Dir: true})
	}
	return entries
}

func splitPath(p string) []string {
	var parts []string
	for _, part := range strings.Split(path.Clean(p), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// BuildIndex assembles the multi-platform index that a `docker pull` of the
// tag resolves through.
//
// An index is published even for a single platform. A consumer that finds one
// shape of document on some releases and another shape on others has to handle
// both, and "it depends how many architectures you built" is not a distinction
// worth exporting.
func BuildIndex(images []*Image, annotations map[string]string) (Blob, error) {
	if len(images) == 0 {
		return Blob{}, fmt.Errorf("oci: an index needs at least one image")
	}

	descriptors := make([]Descriptor, 0, len(images))
	for _, img := range images {
		descriptors = append(descriptors, img.Descriptor)
	}
	// Sorted so that the index — and therefore the image's published digest —
	// does not depend on the order the builds happened to finish in.
	sort.Slice(descriptors, func(i, j int) bool {
		return platformOf(descriptors[i]) < platformOf(descriptors[j])
	})

	index := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeIndex,
		Manifests:     descriptors,
		Annotations:   annotations,
	}

	data, err := encode(index)
	if err != nil {
		return Blob{}, err
	}
	return Blob{Digest: DigestOf(data), MediaType: MediaTypeIndex, Content: data}, nil
}

// Annotations returns the standard OCI provenance labels for a release.
func Annotations(source, revision, version, title, description string, created time.Time) map[string]string {
	out := map[string]string{
		"org.opencontainers.image.created":  created.UTC().Format(time.RFC3339),
		"org.opencontainers.image.source":   source,
		"org.opencontainers.image.revision": revision,
		"org.opencontainers.image.version":  version,
		"org.opencontainers.image.title":    title,
	}
	if description != "" {
		out["org.opencontainers.image.description"] = description
	}
	for key, value := range out {
		if value == "" {
			delete(out, key)
		}
	}
	return out
}

func platformOf(d Descriptor) string {
	if d.Platform == nil {
		return ""
	}
	return d.Platform.String()
}
