package oci_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

// rawLayout writes a layout by hand, blob by blob, for the shapes a builder
// would never write on purpose.
type rawLayout struct {
	t   *testing.T
	dir string
}

func newRawLayout(t *testing.T) rawLayout {
	t.Helper()
	l := rawLayout{t: t, dir: t.TempDir()}
	l.file("oci-layout", `{"imageLayoutVersion":"1.0.0"}`)
	return l
}

func (l rawLayout) file(name, content string) {
	l.t.Helper()
	path := filepath.Join(l.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l rawLayout) blob(mediaType, content string) oci.Descriptor {
	l.t.Helper()
	d := oci.DigestOf([]byte(content))
	l.file(filepath.Join("blobs", "sha256", d.Hex()), content)
	return oci.Descriptor{MediaType: mediaType, Digest: d, Size: int64(len(content))}
}

func (l rawLayout) json(mediaType string, v any) oci.Descriptor {
	l.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		l.t.Fatal(err)
	}
	return l.blob(mediaType, string(data))
}

func (l rawLayout) index(entries ...oci.Descriptor) {
	l.t.Helper()
	data, err := json.Marshal(oci.Index{SchemaVersion: 2, MediaType: oci.MediaTypeIndex, Manifests: entries})
	if err != nil {
		l.t.Fatal(err)
	}
	l.file("index.json", string(data))
}

// image writes a config and a layer and returns the manifest's descriptor,
// with config as given so a test can break it.
func (l rawLayout) image(config string) oci.Descriptor {
	l.t.Helper()
	layer := l.blob(oci.MediaTypeLayerGzip, "a layer")
	cfg := l.blob(oci.MediaTypeConfig, config)
	return l.json(oci.MediaTypeManifest, oci.Manifest{
		SchemaVersion: 2, MediaType: oci.MediaTypeManifest, Config: cfg, Layers: []oci.Descriptor{layer},
	})
}

func TestLoadLayoutRefusesWhatIsNotAnImage(t *testing.T) {
	const linuxConfig = `{"os":"linux","architecture":"amd64"}`

	for _, tc := range []struct {
		name  string
		build func(l rawLayout)
		want  string
	}{
		{"an unsupported layout version", func(l rawLayout) {
			l.file("oci-layout", `{"imageLayoutVersion":"2.0.0"}`)
			l.index(l.image(linuxConfig))
		}, "unsupported oci-layout"},
		{"no index.json", func(rawLayout) {}, "index.json"},
		{"an index.json that is not JSON", func(l rawLayout) { l.file("index.json", "{") }, "index.json"},
		{"an empty index", func(l rawLayout) { l.index() }, "holds no image"},
		{"a media type that is not an image", func(l rawLayout) {
			l.index(l.blob("application/vnd.example.thing", "x"))
		}, "which is not an image"},
		{"indexes nested three deep", func(l rawLayout) {
			inner := l.json(oci.MediaTypeIndex, oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{l.image(linuxConfig)}})
			middle := l.json(oci.MediaTypeIndex, oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{inner}})
			top := l.json(oci.MediaTypeIndex, oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{middle}})
			l.index(top)
		}, "nest too deeply"},
		{"a nested index that is not JSON", func(l rawLayout) {
			l.index(l.blob(oci.MediaTypeIndex, "{"))
		}, "index"},
		{"a nested index that is missing", func(l rawLayout) {
			l.index(oci.Descriptor{MediaType: oci.MediaTypeIndex, Digest: oci.DigestOf([]byte("gone")), Size: 4})
		}, "blob"},
		{"a manifest that is not JSON", func(l rawLayout) {
			l.index(l.blob(oci.MediaTypeManifest, "{"))
		}, "manifest"},
		{"a config naming no platform", func(l rawLayout) {
			l.index(l.image(`{"rootfs":{"type":"layers"}}`))
		}, "names no platform"},
		{"a config that is not JSON", func(l rawLayout) {
			l.index(l.image("{"))
		}, "config"},
		{"a blob whose size is wrong", func(l rawLayout) {
			layer := l.blob(oci.MediaTypeLayerGzip, "a layer")
			layer.Size++
			cfg := l.blob(oci.MediaTypeConfig, linuxConfig)
			l.index(l.json(oci.MediaTypeManifest, oci.Manifest{
				SchemaVersion: 2, MediaType: oci.MediaTypeManifest, Config: cfg, Layers: []oci.Descriptor{layer},
			}))
		}, "bytes, not the"},
		{"a blob that is not what its digest says", func(l rawLayout) {
			manifest := l.image(linuxConfig)
			l.file(filepath.Join("blobs", "sha256", manifest.Digest.Hex()), `{"tampered":true}`)
			l.index(manifest)
		}, "holds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newRawLayout(t)
			tc.build(l)

			_, err := oci.LoadLayout(l.dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// A builder listing one image twice gets it pushed once.
func TestLoadLayoutKeepsEachManifestOnce(t *testing.T) {
	l := newRawLayout(t)
	image := l.image(`{"os":"linux","architecture":"arm64","variant":"v8"}`)
	l.index(image, image)

	layout, err := oci.LoadLayout(l.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Manifests) != 1 || layout.Platforms[0].String() != "linux/arm64/v8" {
		t.Errorf("layout = %+v", layout)
	}
}

func TestPushLayoutRefusesAnIncompletePush(t *testing.T) {
	l := newRawLayout(t)
	l.index(l.image(`{"os":"linux","architecture":"amd64"}`))
	layout, err := oci.LoadLayout(l.dir)
	if err != nil {
		t.Fatal(err)
	}
	index, err := layout.Index(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := oci.NewRegistry("registry.test")

	for name, o := range map[string]oci.LayoutPushOptions{
		"no registry": {Repository: "you/tool", Tags: []string{"1"}, Layout: layout, Index: index},
		"no layout":   {Registry: reg, Repository: "you/tool", Tags: []string{"1"}, Index: index},
		"no tags":     {Registry: reg, Repository: "you/tool", Layout: layout, Index: index},
	} {
		if _, err := oci.PushLayout(context.Background(), o); err == nil {
			t.Errorf("%s: PushLayout went ahead", name)
		}
	}
}
