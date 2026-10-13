package oci_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/oci/ocitest"
)

var (
	amd64 = oci.Platform{OS: "linux", Architecture: "amd64"}
	arm64 = oci.Platform{OS: "linux", Architecture: "arm64"}
)

// What buildx writes for a multi-platform build with provenance: an index
// pointing at an index of images and their attestations.
func TestLoadLayoutReadsANestedIndex(t *testing.T) {
	dir := t.TempDir()
	images := ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{Platforms: []oci.Platform{amd64, arm64}, Attest: true})

	l, err := oci.LoadLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Manifests) != 4 {
		t.Errorf("manifests = %d, want two images and two attestations", len(l.Manifests))
	}
	if got := platforms(l); got != "linux/amd64,linux/arm64" {
		t.Errorf("platforms = %s; attestations are not runnable", got)
	}
	for _, want := range images {
		if !lists(l, want) {
			t.Errorf("image %s is missing", want.Short())
		}
	}
}

// A single-platform build may list its manifest directly, with the platform
// only in the config.
func TestLoadLayoutTakesThePlatformFromTheConfig(t *testing.T) {
	dir := t.TempDir()
	ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{Platforms: []oci.Platform{arm64}, Flat: true})

	l, err := oci.LoadLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := platforms(l); got != "linux/arm64" {
		t.Errorf("platforms = %s", got)
	}
	if l.Manifests[0].Platform == nil {
		t.Error("the descriptor the index will list has no platform")
	}
}

func TestLoadLayoutRefusesAnIncompleteLayout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, dir string)
		want   string
	}{
		{"not a layout", func(t *testing.T, dir string) { t.Helper(); remove(t, dir, "oci-layout") }, "not an OCI image layout"},
		{"a missing blob", func(t *testing.T, dir string) { t.Helper(); removeALayer(t, dir) }, "missing blob"},
		{"only attestations", func(t *testing.T, dir string) {
			t.Helper()
			for _, entry := range blobs(t, dir) {
				os.Remove(entry)
			}
			ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{
				Platforms: []oci.Platform{{OS: "unknown", Architecture: "unknown"}},
			})
		}, "no runnable image"},
		{"a digest that is a path", func(t *testing.T, dir string) {
			t.Helper()
			write(t, dir, "index.json", `{"schemaVersion":2,"manifests":[{"mediaType":"`+oci.MediaTypeManifest+
				`","digest":"sha256:../../../etc/passwd","size":1}]}`)
		}, "not a sha256 digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{Platforms: []oci.Platform{amd64}})
			tc.damage(t, dir)

			_, err := oci.LoadLayout(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// The index is the release's, not the builder's: its annotations are the
// ones a floating tag reads a version from, and the same manifests in another
// order make the same digest.
func TestLayoutIndexCarriesTheReleaseAnnotations(t *testing.T) {
	dir := t.TempDir()
	ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{Platforms: []oci.Platform{arm64, amd64}, Attest: true})
	l, err := oci.LoadLayout(dir)
	if err != nil {
		t.Fatal(err)
	}

	index, err := l.Index(map[string]string{"org.opencontainers.image.version": "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	var doc oci.Index
	if err := json.Unmarshal(index.Content, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Annotations["org.opencontainers.image.version"] != "1.2.3" {
		t.Errorf("annotations = %v", doc.Annotations)
	}

	l.Manifests[0], l.Manifests[len(l.Manifests)-1] = l.Manifests[len(l.Manifests)-1], l.Manifests[0]
	again, err := l.Index(map[string]string{"org.opencontainers.image.version": "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Digest != index.Digest {
		t.Error("the index digest depends on the order the builder listed manifests in")
	}
}

func TestPushLayoutPublishesEveryManifestAndTheIndex(t *testing.T) {
	fake := ocitest.New(t)
	dir := t.TempDir()
	ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{Platforms: []oci.Platform{amd64, arm64}, Attest: true})
	l, err := oci.LoadLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	index, err := l.Index(nil)
	if err != nil {
		t.Fatal(err)
	}

	push := func() *oci.PushResult {
		result, err := oci.PushLayout(context.Background(), oci.LayoutPushOptions{
			Registry: fake.Client(), Repository: "you/talos", Tags: []string{"1.2.3-fixer", "latest-fixer"},
			Layout: l, Index: index,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := push()

	for _, d := range l.Manifests {
		fake.Mu.Lock()
		_, ok := fake.Manifests["you/talos/"+string(d.Digest)]
		fake.Mu.Unlock()
		if !ok {
			t.Errorf("manifest %s was not pushed", d.Digest.Short())
		}
	}
	for _, tag := range []string{"1.2.3-fixer", "latest-fixer"} {
		fake.Mu.Lock()
		published := fake.Manifests["you/talos/"+tag]
		fake.Mu.Unlock()
		if oci.DigestOf(published) != index.Digest {
			t.Errorf("%s does not name the release's index", tag)
		}
	}
	if result.Uploaded != 8 || strings.Join(result.Platforms, ",") != "linux/amd64,linux/arm64" {
		t.Errorf("result = %+v, want 8 blobs uploaded for linux/amd64 and linux/arm64", result)
	}

	if again := push(); again.Uploaded != 0 {
		t.Errorf("a re-run uploaded %d blobs, want none", again.Uploaded)
	}
}

func platforms(l *oci.Layout) string {
	out := make([]string, 0, len(l.Platforms))
	for _, p := range l.Platforms {
		out = append(out, p.String())
	}
	return strings.Join(out, ",")
}

func lists(l *oci.Layout, d oci.Digest) bool {
	for _, m := range l.Manifests {
		if m.Digest == d {
			return true
		}
	}
	return false
}

func blobs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "blobs", "sha256", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func remove(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

// removeALayer deletes the one blob that is neither JSON nor referenced from
// index.json directly: the layer.
func removeALayer(t *testing.T, dir string) {
	t.Helper()
	for _, path := range blobs(t, dir) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(string(data), "layer for") {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("no layer to remove")
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
