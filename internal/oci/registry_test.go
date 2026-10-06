package oci_test

import (
	"context"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci/ocitest"

	"github.com/danielriddell21/letsgo/internal/oci"
)

func TestPushPublishesEveryBlobAndTheIndex(t *testing.T) {
	fake := ocitest.New(t)
	reg := fake.Client()

	o := options(t)
	amd64, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}
	// A different binary, as a real matrix produces: two platforms sharing one
	// layer would hide whether each image's blobs were pushed.
	o.Binary = binary(t, "different ELF-ish bytes")
	o.Platform = oci.Platform{OS: "linux", Architecture: "arm64"}
	arm64, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	result, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3", "latest"},
		Images: []*oci.Image{amd64, arm64}, Index: indexOf(t, amd64, arm64),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, img := range []*oci.Image{amd64, arm64} {
		if !fake.Has("you/tool", img.Layer.Digest) {
			t.Errorf("%s: layer was not uploaded", img.Platform)
		}
		if !fake.Has("you/tool", img.ConfigJS.Digest) {
			t.Errorf("%s: config was not uploaded", img.Platform)
		}
	}

	// Every tag must name the same bytes as the index the release assembled and
	// recorded. Rebuilding it here from anything less than identical inputs
	// would publish a digest the manifest does not claim, and `latest` would
	// disagree with the version tag.
	want := indexOf(t, amd64, arm64)
	for _, tag := range []string{"1.2.3", "latest"} {
		fake.Mu.Lock()
		published, ok := fake.Manifests["you/tool/"+tag]
		fake.Mu.Unlock()

		if !ok {
			t.Fatalf("%s was not published", tag)
		}
		if oci.DigestOf(published) != want.Digest {
			t.Errorf("%s is %s, not the assembled index %s",
				tag, oci.DigestOf(published), want.Digest)
		}
	}
	if result.Digest != want.Digest {
		t.Errorf("reported digest %s does not name the published index", result.Digest)
	}
	if strings.Join(result.Platforms, ",") != "linux/amd64,linux/arm64" {
		t.Errorf("platforms = %v", result.Platforms)
	}
	if result.Uploaded != 4 {
		t.Errorf("uploaded %d blobs, want 4", result.Uploaded)
	}
	// The token must be fetched once and reused, not re-fetched per request.
	if fake.TokenIssued != 1 {
		t.Errorf("issued %d tokens, want 1", fake.TokenIssued)
	}
}

// Re-running a release must move no bytes: every blob is content-addressed and
// already there.
func TestPushIsIdempotent(t *testing.T) {
	fake := ocitest.New(t)
	reg := fake.Client()

	img, err := oci.BuildImage(options(t))
	if err != nil {
		t.Fatal(err)
	}
	push := func() *oci.PushResult {
		t.Helper()
		result, err := oci.Push(context.Background(), oci.PushOptions{
			Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
			Images: []*oci.Image{img}, Index: indexOf(t, img),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	first, second := push(), push()

	if first.Digest != second.Digest {
		t.Errorf("two pushes produced different digests: %s and %s", first.Digest, second.Digest)
	}
	if second.Uploaded != 0 || second.Skipped != 2 {
		t.Errorf("the second push uploaded %d and skipped %d, want 0 and 2",
			second.Uploaded, second.Skipped)
	}
}

func TestPushMountsBaseLayersRatherThanCopyingThem(t *testing.T) {
	fake := ocitest.New(t)
	reg := fake.Client()

	baseLayer := []byte("base layer bytes")
	baseDigest := oci.DigestOf(baseLayer)
	fake.Mu.Lock()
	fake.Blobs["distroless/static/"+string(baseDigest)] = baseLayer
	fake.Mu.Unlock()

	o := options(t)
	o.Base = &oci.Base{
		Reference: oci.Reference{Registry: oci.DefaultRegistry, Repository: "distroless/static"},
		Config:    oci.Config{RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}}},
		Layers: []oci.Descriptor{{
			MediaType: oci.MediaTypeLayerGzip, Digest: baseDigest, Size: int64(len(baseLayer)),
		}},
	}
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	result, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
		Base: &oci.Source{Registry: reg, Repository: "distroless/static"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !fake.Has("you/tool", baseDigest) {
		t.Error("the base layer never reached the target repository")
	}
	// A mount transfers nothing, so the base layer must not be counted as an
	// upload.
	if result.Uploaded != 2 {
		t.Errorf("uploaded %d blobs, want 2 (ours only)", result.Uploaded)
	}
}

// A registry may decline a mount for any reason; the layer still has to
// arrive.
func TestPushFallsBackWhenAMountIsRefused(t *testing.T) {
	fake := ocitest.New(t)
	fake.RefuseMounts = true
	reg := fake.Client()

	baseLayer := []byte("base layer bytes")
	baseDigest := oci.DigestOf(baseLayer)
	fake.Mu.Lock()
	fake.Blobs["distroless/static/"+string(baseDigest)] = baseLayer
	fake.Mu.Unlock()

	o := options(t)
	o.Base = &oci.Base{
		Reference: oci.Reference{Registry: oci.DefaultRegistry, Repository: "distroless/static"},
		Config:    oci.Config{RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}}},
		Layers: []oci.Descriptor{{
			MediaType: oci.MediaTypeLayerGzip, Digest: baseDigest, Size: int64(len(baseLayer)),
		}},
	}
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
		Base: &oci.Source{Registry: reg, Repository: "distroless/static"},
	}); err != nil {
		t.Fatal(err)
	}

	if !fake.Has("you/tool", baseDigest) {
		t.Error("the base layer never reached the target repository")
	}
}

// Stacking on a base whose blobs are unreachable must fail loudly rather than
// publishing a manifest naming layers the registry does not hold.
func TestPushRefusesAnUnreachableBase(t *testing.T) {
	fake := ocitest.New(t)

	o := options(t)
	o.Base = &oci.Base{
		Reference: oci.Reference{Registry: oci.DefaultRegistry, Repository: "distroless/static"},
		Config:    oci.Config{RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}}},
		Layers:    []oci.Descriptor{{MediaType: oci.MediaTypeLayerGzip, Digest: oci.Digest("sha256:" + strings.Repeat("a", 64))}},
	}
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	_, err = oci.Push(context.Background(), oci.PushOptions{
		Registry: fake.Client(), Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
	})
	if err == nil {
		t.Fatal("want an error when a base layer cannot be fetched")
	}
}

// indexOf assembles the index for images, as a release does before pushing.
func indexOf(t *testing.T, images ...*oci.Image) oci.Blob {
	t.Helper()
	index, err := oci.BuildIndex(images, nil)
	if err != nil {
		t.Fatal(err)
	}
	return index
}
