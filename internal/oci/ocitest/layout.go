package ocitest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

// LayoutOptions shape a layout the way a builder writes one.
type LayoutOptions struct {
	// Platforms get one image each, every one with its own layer.
	Platforms []oci.Platform

	// Attest adds an attestation manifest per image, as buildx does with
	// provenance on.
	Attest bool

	// Flat lists a single image's manifest directly in index.json, with no
	// platform on its descriptor, as a single-platform build can.
	Flat bool
}

// WriteLayout writes an OCI image layout into dir, as `docker buildx build
// --output type=oci,tar=false` would, and returns the digests of the image
// manifests it wrote.
func WriteLayout(tb testing.TB, dir string, o LayoutOptions) []oci.Digest {
	tb.Helper()
	w := layoutWriter{tb: tb, dir: dir}
	w.file("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))

	var images, entries []oci.Descriptor
	digests := make([]oci.Digest, 0, len(o.Platforms))
	for _, p := range o.Platforms {
		manifest := w.image(p, "layer for "+p.String())
		digests = append(digests, manifest.Digest)
		images = append(images, manifest)

		if o.Attest {
			attest := w.image(oci.Platform{OS: "unknown", Architecture: "unknown"}, "provenance for "+p.String())
			attest.Annotations = map[string]string{
				"vnd.docker.reference.digest": string(manifest.Digest),
				"vnd.docker.reference.type":   "attestation-manifest",
			}
			images = append(images, attest)
		}
	}

	if o.Flat {
		if len(images) != 1 {
			tb.Fatal("ocitest: a flat layout holds one image")
		}
		flat := images[0]
		flat.Platform = nil
		entries = []oci.Descriptor{flat}
	} else {
		entries = []oci.Descriptor{w.blob(oci.MediaTypeIndex, oci.Index{
			SchemaVersion: 2, MediaType: oci.MediaTypeIndex, Manifests: images,
		})}
	}

	data, err := json.Marshal(oci.Index{SchemaVersion: 2, MediaType: oci.MediaTypeIndex, Manifests: entries})
	if err != nil {
		tb.Fatal(err)
	}
	w.file("index.json", data)
	return digests
}

type layoutWriter struct {
	tb  testing.TB
	dir string
}

func (w layoutWriter) image(p oci.Platform, layer string) oci.Descriptor {
	layerDesc := w.raw(oci.MediaTypeLayerGzip, []byte(layer))
	config := w.blob(oci.MediaTypeConfig, oci.Config{
		OS: p.OS, Architecture: p.Architecture, Variant: p.Variant,
		RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{layerDesc.Digest}},
	})
	manifest := w.blob(oci.MediaTypeManifest, oci.Manifest{
		SchemaVersion: 2, MediaType: oci.MediaTypeManifest, Config: config, Layers: []oci.Descriptor{layerDesc},
	})
	manifest.Platform = &p
	return manifest
}

func (w layoutWriter) blob(mediaType string, v any) oci.Descriptor {
	data, err := json.Marshal(v)
	if err != nil {
		w.tb.Fatal(err)
	}
	return w.raw(mediaType, data)
}

func (w layoutWriter) raw(mediaType string, data []byte) oci.Descriptor {
	d := oci.DigestOf(data)
	w.file(filepath.Join("blobs", "sha256", d.Hex()), data)
	return oci.Descriptor{MediaType: mediaType, Digest: d, Size: int64(len(data))}
}

func (w layoutWriter) file(name string, data []byte) {
	path := filepath.Join(w.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		w.tb.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		w.tb.Fatal(err)
	}
}
