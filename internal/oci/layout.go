package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Layout is an image someone else built, read from an OCI image layout on
// disk: what `docker buildx build --output type=oci,tar=false` writes.
//
// This package builds its own images and can say exactly what is in them. A
// layout is the opposite case — an images-hook plugin built it, from a
// Dockerfile, in a way nothing here can replay — so all that is kept is what
// is needed to push it faithfully: the per-platform manifests, and where their
// blobs are.
//
// The layout's own top-level index is not pushed. The release builds a new one
// over the same manifests, carrying the same provenance annotations its own
// images carry, which is what lets a floating tag compare versions.
type Layout struct {
	Dir string

	// Manifests are the image manifests the release's index lists, the
	// attestation manifests a builder attaches included.
	Manifests []Descriptor

	// Platforms are the ones an image actually runs on, sorted. Attestations
	// carry "unknown/unknown" and are left out.
	Platforms []Platform
}

// layoutDigest is the only digest form read from a layout. It names a path on
// disk, so anything looser would be a way out of the directory.
var layoutDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// LoadLayout reads an OCI image layout, checking that every blob the image
// needs is present before anything is pushed.
func LoadLayout(dir string) (*Layout, error) {
	marker, err := os.ReadFile(filepath.Join(dir, "oci-layout"))
	if err != nil {
		return nil, fmt.Errorf("oci: %s is not an OCI image layout: %w", dir, err)
	}
	var version struct {
		ImageLayoutVersion string `json:"imageLayoutVersion"`
	}
	if err := json.Unmarshal(marker, &version); err != nil || version.ImageLayoutVersion != "1.0.0" {
		return nil, fmt.Errorf("oci: %s: unsupported oci-layout %q", dir, marker)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("oci: %s: %w", dir, err)
	}
	var top Index
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("oci: %s: index.json: %w", dir, err)
	}
	if len(top.Manifests) == 0 {
		return nil, fmt.Errorf("oci: %s holds no image", dir)
	}

	l := &Layout{Dir: dir}
	for _, d := range top.Manifests {
		if err := l.collect(d, 0); err != nil {
			return nil, err
		}
	}

	for _, d := range l.Manifests {
		if d.Platform != nil && d.Platform.OS != "unknown" {
			l.Platforms = append(l.Platforms, *d.Platform)
		}
	}
	if len(l.Platforms) == 0 {
		return nil, fmt.Errorf("oci: %s holds no runnable image, only attestations", dir)
	}
	sort.Slice(l.Platforms, func(i, j int) bool { return l.Platforms[i].String() < l.Platforms[j].String() })
	return l, nil
}

// collect walks one descriptor down to the image manifests under it.
//
// Nesting is bounded: a builder writes an index pointing at an index at most,
// and a deeper or cyclic layout is not one this was asked to push.
func (l *Layout) collect(d Descriptor, depth int) error {
	switch d.MediaType {
	case MediaTypeIndex, MediaTypeDockerList:
		return l.collectIndex(d, depth)
	case MediaTypeManifest, MediaTypeDockerMani:
		return l.collectManifest(d)
	default:
		return fmt.Errorf("oci: %s: %s has media type %q, which is not an image", l.Dir, d.Digest.Short(), d.MediaType)
	}
}

func (l *Layout) collectIndex(d Descriptor, depth int) error {
	if depth > 1 {
		return fmt.Errorf("oci: %s: indexes nest too deeply", l.Dir)
	}
	blob, err := l.Blob(d.Digest)
	if err != nil {
		return err
	}
	var index Index
	if err := json.Unmarshal(blob.Content, &index); err != nil {
		return fmt.Errorf("oci: %s: index %s: %w", l.Dir, d.Digest.Short(), err)
	}
	for _, child := range index.Manifests {
		if err := l.collect(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// collectManifest keeps one image manifest, once its blobs are all there.
func (l *Layout) collectManifest(d Descriptor) error {
	m, err := l.manifest(d)
	if err != nil {
		return err
	}
	for _, blob := range append([]Descriptor{m.Config}, m.Layers...) {
		if err := l.present(blob); err != nil {
			return err
		}
	}
	if d.Platform == nil {
		// A single-platform build may leave the platform to the config.
		platform, err := l.platformOf(m.Config)
		if err != nil {
			return err
		}
		d.Platform = &platform
	}
	for _, seen := range l.Manifests {
		if seen.Digest == d.Digest {
			return nil
		}
	}
	l.Manifests = append(l.Manifests, d)
	return nil
}

func (l *Layout) manifest(d Descriptor) (Manifest, error) {
	blob, err := l.Blob(d.Digest)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(blob.Content, &m); err != nil {
		return Manifest{}, fmt.Errorf("oci: %s: manifest %s: %w", l.Dir, d.Digest.Short(), err)
	}
	return m, nil
}

func (l *Layout) platformOf(config Descriptor) (Platform, error) {
	blob, err := l.Blob(config.Digest)
	if err != nil {
		return Platform{}, err
	}
	var c Config
	if err := json.Unmarshal(blob.Content, &c); err != nil {
		return Platform{}, fmt.Errorf("oci: %s: config %s: %w", l.Dir, config.Digest.Short(), err)
	}
	if c.OS == "" || c.Architecture == "" {
		return Platform{}, fmt.Errorf("oci: %s: config %s names no platform", l.Dir, config.Digest.Short())
	}
	return Platform{OS: c.OS, Architecture: c.Architecture, Variant: c.Variant}, nil
}

// path is where a blob lives in the layout.
func (l *Layout) path(digest Digest) (string, error) {
	if !layoutDigest.MatchString(string(digest)) {
		return "", fmt.Errorf("oci: %s: %q is not a sha256 digest", l.Dir, digest)
	}
	return filepath.Join(l.Dir, "blobs", "sha256", digest.Hex()), nil
}

// present checks a blob is there, at the size its descriptor claims, without
// reading it: layers are the large part of an image, and they are read once,
// when they are pushed.
func (l *Layout) present(d Descriptor) error {
	path, err := l.path(d.Digest)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("oci: %s is missing blob %s", l.Dir, d.Digest.Short())
	}
	if err != nil {
		return fmt.Errorf("oci: %w", err)
	}
	if info.Size() != d.Size {
		return fmt.Errorf("oci: %s: blob %s is %d bytes, not the %d its descriptor says",
			l.Dir, d.Digest.Short(), info.Size(), d.Size)
	}
	return nil
}

// Blob reads one blob and checks it is what its digest says.
func (l *Layout) Blob(digest Digest) (Blob, error) {
	path, err := l.path(digest)
	if err != nil {
		return Blob{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Blob{}, fmt.Errorf("oci: %s: blob %s: %w", l.Dir, digest.Short(), err)
	}
	if got := DigestOf(content); got != digest {
		return Blob{}, fmt.Errorf("oci: %s: blob %s holds %s", l.Dir, digest.Short(), got.Short())
	}
	return Blob{Digest: digest, Content: content}, nil
}

// Index builds the index the release publishes over the layout's manifests,
// with the release's own annotations. Sorted, so the digest does not depend
// on the order a builder happened to list them in.
func (l *Layout) Index(annotations map[string]string) (Blob, error) {
	descriptors := append([]Descriptor{}, l.Manifests...)
	sort.SliceStable(descriptors, func(i, j int) bool {
		pi, pj := platformOf(descriptors[i]), platformOf(descriptors[j])
		if pi != pj {
			return pi < pj
		}
		return descriptors[i].Digest < descriptors[j].Digest
	})

	data, err := encode(Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeIndex,
		Manifests:     descriptors,
		Annotations:   annotations,
	})
	if err != nil {
		return Blob{}, err
	}
	return Blob{Digest: DigestOf(data), MediaType: MediaTypeIndex, Content: data}, nil
}

// LayoutPushOptions describe publishing a layout.
type LayoutPushOptions struct {
	Registry   *Registry
	Repository string
	Tags       []string

	Layout *Layout

	// Index is Layout.Index's result: built during the release and already
	// recorded, so it is passed in rather than built again.
	Index Blob
}

// PushLayout publishes a layout's blobs and manifests, then the index under
// every tag. The same order as Push, for the same reasons: a registry rejects
// a manifest naming a blob it does not hold, and a failed run leaves no tag
// pointing at anything incomplete.
func PushLayout(ctx context.Context, o LayoutPushOptions) (*PushResult, error) {
	switch {
	case o.Registry == nil || o.Repository == "":
		return nil, fmt.Errorf("oci: a registry and a repository are required")
	case o.Layout == nil || o.Index.Digest == "":
		return nil, fmt.Errorf("oci: nothing to push")
	case len(o.Tags) == 0:
		return nil, fmt.Errorf("oci: an image needs at least one tag")
	}

	result := &PushResult{}

	for _, d := range o.Layout.Manifests {
		m, err := o.Layout.manifest(d)
		if err != nil {
			return nil, err
		}
		for _, ref := range append([]Descriptor{m.Config}, m.Layers...) {
			if err := o.pushBlob(ctx, ref, result); err != nil {
				return nil, err
			}
		}

		content, err := o.Layout.Blob(d.Digest)
		if err != nil {
			return nil, err
		}
		content.MediaType = d.MediaType
		if err := o.Registry.PushManifest(ctx, o.Repository, string(d.Digest), content); err != nil {
			return nil, err
		}
	}

	for _, tag := range o.Tags {
		if err := o.Registry.PushManifest(ctx, o.Repository, tag, o.Index); err != nil {
			return nil, err
		}
	}

	for _, p := range o.Layout.Platforms {
		result.Platforms = append(result.Platforms, p.String())
	}
	result.Digest = o.Index.Digest
	result.Tags = o.Tags
	return result, nil
}

// pushBlob uploads one blob unless the registry already holds it, reading it
// from disk only then.
func (o LayoutPushOptions) pushBlob(ctx context.Context, ref Descriptor, result *PushResult) error {
	present, err := o.Registry.HasBlob(ctx, o.Repository, ref.Digest)
	if err != nil {
		return err
	}
	if present {
		result.Skipped++
		return nil
	}
	blob, err := o.Layout.Blob(ref.Digest)
	if err != nil {
		return err
	}
	blob.MediaType = ref.MediaType

	location, err := o.Registry.startUpload(ctx, o.Repository, "")
	if err != nil {
		return err
	}
	if location == "" {
		result.Skipped++
		return nil
	}
	if err := o.Registry.finishUpload(ctx, o.Repository, location, blob); err != nil {
		return err
	}
	result.Uploaded++
	return nil
}
