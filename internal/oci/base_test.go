package oci_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

// seedBase puts a two-platform base image into the fake registry and returns
// its repository.
func seedBase(t *testing.T, fake *fakeRegistry) string {
	t.Helper()
	const repo = "distroless/static"

	var manifests []oci.Descriptor
	for _, arch := range []string{"amd64", "arm64"} {
		layer := []byte("layer for " + arch)
		config, err := json.Marshal(oci.Config{
			Architecture: arch, OS: "linux",
			Config: oci.RunConfig{Env: []string{"PATH=/usr/local/bin"}, User: "nonroot"},
			RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{oci.DigestOf(layer)}},
		})
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := json.Marshal(oci.Manifest{
			SchemaVersion: 2, MediaType: oci.MediaTypeManifest,
			Config: oci.Descriptor{
				MediaType: oci.MediaTypeConfig, Digest: oci.DigestOf(config), Size: int64(len(config)),
			},
			Layers: []oci.Descriptor{{
				MediaType: oci.MediaTypeLayerGzip, Digest: oci.DigestOf(layer), Size: int64(len(layer)),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}

		fake.mu.Lock()
		fake.blobs[repo+"/"+string(oci.DigestOf(layer))] = layer
		fake.blobs[repo+"/"+string(oci.DigestOf(config))] = config
		fake.manifests[repo+"/"+string(oci.DigestOf(manifest))] = manifest
		fake.mu.Unlock()

		manifests = append(manifests, oci.Descriptor{
			MediaType: oci.MediaTypeManifest,
			Digest:    oci.DigestOf(manifest),
			Size:      int64(len(manifest)),
			Platform:  &oci.Platform{OS: "linux", Architecture: arch},
		})
	}

	// Buildx attaches attestation manifests with a placeholder platform.
	// Selecting one would produce an image that cannot run.
	manifests = append(manifests, oci.Descriptor{
		MediaType: oci.MediaTypeManifest,
		Digest:    oci.DigestOf([]byte("attestation")),
		Platform:  &oci.Platform{OS: "unknown", Architecture: "unknown"},
	})

	index, err := json.Marshal(oci.Index{
		SchemaVersion: 2, MediaType: oci.MediaTypeIndex, Manifests: manifests,
	})
	if err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	fake.manifests[repo+"/nonroot"] = index
	fake.mu.Unlock()

	return repo
}

func TestResolveBaseSelectsThePlatform(t *testing.T) {
	fake := newFakeRegistry(t)
	repo := seedBase(t, fake)

	ref, err := oci.ParseReference("gcr.io/" + repo + ":nonroot")
	if err != nil {
		t.Fatal(err)
	}

	base, err := oci.ResolveBase(context.Background(), fake.client(), ref,
		oci.Platform{OS: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}

	if base.Config.Architecture != "arm64" {
		t.Errorf("resolved %s, want arm64", base.Config.Architecture)
	}
	if base.Config.Config.User != "nonroot" {
		t.Errorf("run config = %+v", base.Config.Config)
	}
	if len(base.Layers) != 1 {
		t.Fatalf("layers = %+v", base.Layers)
	}
	// The digest names the per-platform manifest actually received, not the
	// index and not whatever the config file said.
	if base.Digest == "" || !strings.HasPrefix(string(base.Digest), "sha256:") {
		t.Errorf("digest = %q", base.Digest)
	}
}

func TestResolveBaseReportsAMissingPlatform(t *testing.T) {
	fake := newFakeRegistry(t)
	repo := seedBase(t, fake)

	ref, err := oci.ParseReference("gcr.io/" + repo + ":nonroot")
	if err != nil {
		t.Fatal(err)
	}

	_, err = oci.ResolveBase(context.Background(), fake.client(), ref,
		oci.Platform{OS: "linux", Architecture: "riscv64"})
	if err == nil {
		t.Fatal("want an error for a platform the base does not have")
	}
	// The message should say what is there, since the fix is to pick one.
	if !strings.Contains(err.Error(), "linux/amd64") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// End to end: resolve a base, stack on it, push, and confirm the base's layers
// arrived alongside ours.
func TestBaseStackedAndPushed(t *testing.T) {
	fake := newFakeRegistry(t)
	repo := seedBase(t, fake)
	reg := fake.client()

	ref, err := oci.ParseReference("gcr.io/" + repo + ":nonroot")
	if err != nil {
		t.Fatal(err)
	}
	base, err := oci.ResolveBase(context.Background(), reg, ref,
		oci.Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}

	o := options(t)
	o.Base = base
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
		Base: &oci.Source{Registry: reg, Repository: repo},
	}); err != nil {
		t.Fatal(err)
	}

	for _, layer := range base.Layers {
		if !fake.has("you/tool", layer.Digest) {
			t.Errorf("base layer %s never reached the target", layer.Digest.Short())
		}
	}
	if !fake.has("you/tool", img.Layer.Digest) {
		t.Error("our own layer never reached the target")
	}
}
