package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/oci"
)

var epoch = time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC)

func binary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func options(t *testing.T) oci.ImageOptions {
	return oci.ImageOptions{
		Binary:   binary(t, "ELF-ish bytes"),
		Name:     "tool",
		Platform: oci.Platform{OS: "linux", Architecture: "amd64"},
		Created:  epoch,
		Annotations: map[string]string{
			"org.opencontainers.image.version": "1.2.3",
		},
	}
}

func TestBuildImageProducesAValidLayer(t *testing.T) {
	img, err := oci.BuildImage(options(t))
	if err != nil {
		t.Fatal(err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(img.Layer.Content))
	if err != nil {
		t.Fatalf("layer is not gzip: %v", err)
	}
	tr := tar.NewReader(zr)

	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("layer is not a tar: %v", err)
		}
		names = append(names, hdr.Name)

		if !hdr.ModTime.Equal(epoch) {
			t.Errorf("%s: mod time %v, want %v", hdr.Name, hdr.ModTime, epoch)
		}
		if hdr.Uid != 0 || hdr.Gid != 0 {
			t.Errorf("%s: ownership %d:%d, want 0:0", hdr.Name, hdr.Uid, hdr.Gid)
		}
	}

	// scratch has no /usr, so every parent has to be in the layer or the
	// binary lands somewhere the runtime has to invent.
	want := []string{"usr/", "usr/local/", "usr/local/bin/", "usr/local/bin/tool"}
	if len(names) != len(want) {
		t.Fatalf("layer contains %v, want %v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("entry %d = %q, want %q", i, names[i], name)
		}
	}
}

// The diff_id must be the digest of the *uncompressed* layer. Getting this
// wrong produces an image that pushes cleanly and cannot be run.
func TestDiffIDIsTheUncompressedDigest(t *testing.T) {
	img, err := oci.BuildImage(options(t))
	if err != nil {
		t.Fatal(err)
	}

	var config oci.Config
	if err := json.Unmarshal(img.ConfigJS.Content, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.RootFS.DiffIDs) != 1 {
		t.Fatalf("diff ids = %v", config.RootFS.DiffIDs)
	}

	zr, err := gzip.NewReader(bytes.NewReader(img.Layer.Content))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := config.RootFS.DiffIDs[0], oci.DigestOf(raw); got != want {
		t.Errorf("diff id = %s, want %s", got, want)
	}
	if img.Layer.Digest != oci.DigestOf(img.Layer.Content) {
		t.Error("the layer descriptor does not name the layer it describes")
	}
}

func TestBuildImageSetsEntrypointAndAnnotations(t *testing.T) {
	img, err := oci.BuildImage(options(t))
	if err != nil {
		t.Fatal(err)
	}

	var config oci.Config
	if err := json.Unmarshal(img.ConfigJS.Content, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Config.Entrypoint) != 1 || config.Config.Entrypoint[0] != "/usr/local/bin/tool" {
		t.Errorf("entrypoint = %v", config.Config.Entrypoint)
	}
	if config.Created != "2024-03-15T12:30:45Z" {
		t.Errorf("created = %q, want the commit time", config.Created)
	}
	if config.Architecture != "amd64" || config.OS != "linux" {
		t.Errorf("platform = %s/%s", config.OS, config.Architecture)
	}
	if config.Config.Labels["org.opencontainers.image.version"] != "1.2.3" {
		t.Errorf("labels = %v", config.Config.Labels)
	}

	var manifest oci.Manifest
	if err := json.Unmarshal(img.Manifest.Content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Config.Digest != img.ConfigJS.Digest {
		t.Error("the manifest does not point at the config it was built with")
	}
	if len(manifest.Layers) != 1 || manifest.Layers[0].Digest != img.Layer.Digest {
		t.Errorf("layers = %+v", manifest.Layers)
	}
}

// Two runs over the same inputs must produce the same image digest, or none
// of the reproducibility claims extend to the container.
func TestBuildImageIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("same bytes"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	first, err := oci.BuildImage(oci.ImageOptions{
		Binary: filepath.Join(dir, "a"), Name: "tool", Created: epoch,
		Platform: oci.Platform{OS: "linux", Architecture: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := oci.BuildImage(oci.ImageOptions{
		Binary: filepath.Join(dir, "b"), Name: "tool", Created: epoch,
		Platform: oci.Platform{OS: "linux", Architecture: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if first.Descriptor.Digest != second.Descriptor.Digest {
		t.Errorf("image digests differ: %s and %s", first.Descriptor.Digest, second.Descriptor.Digest)
	}
}

func TestBuildImageStacksOnABase(t *testing.T) {
	o := options(t)
	o.Base = &oci.Base{
		Reference: "gcr.io/distroless/static@sha256:aaaa",
		Digest:    "sha256:aaaa",
		Config: oci.Config{
			Config: oci.RunConfig{
				Env:  []string{"PATH=/usr/local/bin:/usr/bin"},
				User: "nonroot",
			},
			RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}},
		},
		Layers: []oci.Descriptor{{MediaType: oci.MediaTypeLayerGzip, Digest: "sha256:baselayer", Size: 10}},
	}

	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	var config oci.Config
	if err := json.Unmarshal(img.ConfigJS.Content, &config); err != nil {
		t.Fatal(err)
	}
	// The base's layers stack below ours, in order, or the filesystem the
	// runtime assembles is not the one we described.
	if len(config.RootFS.DiffIDs) != 2 || config.RootFS.DiffIDs[0] != "sha256:base" {
		t.Errorf("diff ids = %v", config.RootFS.DiffIDs)
	}
	// A base that sets Env or User is doing it for a reason; only the
	// entrypoint is ours to override.
	if config.Config.User != "nonroot" || len(config.Config.Env) != 1 {
		t.Errorf("base run config was not inherited: %+v", config.Config)
	}
	if config.Config.Entrypoint[0] != "/usr/local/bin/tool" {
		t.Errorf("entrypoint = %v", config.Config.Entrypoint)
	}

	var manifest oci.Manifest
	if err := json.Unmarshal(img.Manifest.Content, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Layers) != 2 || manifest.Layers[0].Digest != "sha256:baselayer" {
		t.Errorf("layers = %+v", manifest.Layers)
	}
}

func TestBuildIndexSortsAndIsDeterministic(t *testing.T) {
	o := options(t)
	amd64, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}
	o.Platform = oci.Platform{OS: "linux", Architecture: "arm64"}
	arm64, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	first, err := oci.BuildIndex([]*oci.Image{amd64, arm64}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := oci.BuildIndex([]*oci.Image{arm64, amd64}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The published digest must not depend on which build finished first.
	if first.Digest != second.Digest {
		t.Errorf("index digest depends on build order: %s and %s", first.Digest, second.Digest)
	}

	var index oci.Index
	if err := json.Unmarshal(first.Content, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Manifests) != 2 {
		t.Fatalf("manifests = %+v", index.Manifests)
	}
	if index.Manifests[0].Platform.Architecture != "amd64" {
		t.Errorf("manifests are not sorted: %s first", index.Manifests[0].Platform)
	}
	if index.Manifests[0].Digest == index.Manifests[1].Digest {
		t.Error("two architectures produced the same manifest")
	}
}

func TestBuildRejectsIncompleteInput(t *testing.T) {
	if _, err := oci.BuildImage(oci.ImageOptions{Name: "tool"}); err == nil {
		t.Error("want an error without a binary")
	}
	o := options(t)
	o.Platform = oci.Platform{}
	if _, err := oci.BuildImage(o); err == nil {
		t.Error("want an error without a platform")
	}
	if _, err := oci.BuildIndex(nil, nil); err == nil {
		t.Error("want an error for an empty index")
	}
}

func TestDigestHelpers(t *testing.T) {
	d := oci.DigestOf([]byte("hello"))
	if got := string(d); got[:7] != "sha256:" {
		t.Errorf("digest = %q", got)
	}
	if len(d.Hex()) != 64 {
		t.Errorf("hex = %q", d.Hex())
	}
	if len(d.Short()) != 12 {
		t.Errorf("short = %q", d.Short())
	}
}
