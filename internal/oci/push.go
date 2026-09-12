package oci

import (
	"context"
	"fmt"
	"net/url"
	"sort"
)

// PushOptions describe a publication.
type PushOptions struct {
	Registry   *Registry
	Repository string

	// Tags are what the index is published under. The per-platform manifests
	// are published by digest only: a tag for each would put four more names
	// in the registry that nobody should be pulling.
	Tags []string

	Images []*Image

	// Index is the assembled index. It is passed in rather than rebuilt here
	// because it was built during the release, its digest is already recorded
	// in the manifest, and rebuilding it from anything less than identical
	// inputs would publish a digest the release does not claim.
	Index Blob

	// Base, when set, is where the base image's layers can be fetched or
	// mounted from. Without it, an image that stacks on a base would reference
	// blobs the target registry has never seen.
	Base *Source

	// Logf reports progress. Optional.
	Logf func(format string, args ...any)
}

// PushResult is what was published.
type PushResult struct {
	Digest    Digest
	Tags      []string
	Platforms []string

	// Uploaded and Skipped count blobs, which is the only number that says
	// whether a re-run did any work.
	Uploaded int
	Skipped  int
}

// Push publishes every platform's image and the index that ties them together.
//
// Blobs first, then manifests, then the index: a registry rejects a manifest
// naming a blob it does not hold, so the order is not a preference. It also
// makes the operation safely resumable — a failed run leaves blobs behind and
// no tag pointing at anything incomplete.
func Push(ctx context.Context, o PushOptions) (*PushResult, error) {
	if o.Registry == nil || o.Repository == "" {
		return nil, fmt.Errorf("oci: a registry and a repository are required")
	}
	if len(o.Images) == 0 || o.Index.Digest == "" {
		return nil, fmt.Errorf("oci: nothing to push")
	}
	if len(o.Tags) == 0 {
		return nil, fmt.Errorf("oci: an image needs at least one tag")
	}
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	result := &PushResult{}

	for _, img := range o.Images {
		ours := map[Digest]Blob{img.Layer.Digest: img.Layer}

		for _, layer := range layersOf(img) {
			if blob, mine := ours[layer.Digest]; mine {
				if err := o.push(ctx, blob, result); err != nil {
					return nil, err
				}
				continue
			}
			if err := o.copyBase(ctx, layer, result); err != nil {
				return nil, err
			}
		}

		if err := o.push(ctx, img.ConfigJS, result); err != nil {
			return nil, err
		}
		if err := o.Registry.PushManifest(ctx, o.Repository, string(img.Descriptor.Digest), img.Manifest); err != nil {
			return nil, err
		}
		logf("pushed %s (%s)", img.Platform, img.Descriptor.Digest.Short())
		result.Platforms = append(result.Platforms, img.Platform.String())
	}

	// The tags go up last, and each names the same bytes: `latest` is the
	// index the version tag points at, not a second build that happens to be
	// equal.
	for _, tag := range o.Tags {
		if err := o.Registry.PushManifest(ctx, o.Repository, tag, o.Index); err != nil {
			return nil, err
		}
	}

	sort.Strings(result.Platforms)
	result.Digest = o.Index.Digest
	result.Tags = o.Tags
	return result, nil
}

func layersOf(img *Image) []Descriptor {
	var manifest Manifest
	if err := decode(img.Manifest.Content, &manifest); err != nil {
		// The manifest was produced two function calls ago by this package.
		return []Descriptor{{Digest: img.Layer.Digest}}
	}
	return manifest.Layers
}

func (o PushOptions) push(ctx context.Context, blob Blob, result *PushResult) error {
	present, err := o.Registry.HasBlob(ctx, o.Repository, blob.Digest)
	if err != nil {
		return err
	}
	if present {
		result.Skipped++
		return nil
	}
	if err := o.Registry.PushBlob(ctx, o.Repository, blob); err != nil {
		return err
	}
	result.Uploaded++
	return nil
}

// copyBase makes a base image's layer available in the target repository.
//
// A cross-repository mount is tried first, which moves no bytes at all and is
// what makes stacking on a base nearly free when the base lives on the same
// registry. Only when that is refused — or the base is on another registry
// entirely — is the blob actually transferred.
func (o PushOptions) copyBase(ctx context.Context, layer Descriptor, result *PushResult) error {
	present, err := o.Registry.HasBlob(ctx, o.Repository, layer.Digest)
	if err != nil {
		return err
	}
	if present {
		result.Skipped++
		return nil
	}
	if o.Base == nil {
		return fmt.Errorf("oci: layer %s belongs to a base image and there is nowhere to fetch it from",
			layer.Digest.Short())
	}

	if o.Base.Registry.Host == o.Registry.Host {
		mount := url.Values{"mount": {string(layer.Digest)}, "from": {o.Base.Repository}}.Encode()
		location, err := o.Registry.startUpload(ctx, o.Repository, mount)
		if err != nil {
			return err
		}
		if location == "" {
			result.Skipped++
			return nil
		}
		// The registry declined the mount and opened an ordinary upload
		// instead. Finish it rather than starting a second one.
		content, err := o.Base.Registry.Blob(ctx, o.Base.Repository, layer.Digest)
		if err != nil {
			return err
		}
		if err := o.Registry.finishUpload(ctx, o.Repository, location,
			Blob{Digest: layer.Digest, MediaType: layer.MediaType, Content: content}); err != nil {
			return err
		}
		result.Uploaded++
		return nil
	}

	content, err := o.Base.Registry.Blob(ctx, o.Base.Repository, layer.Digest)
	if err != nil {
		return err
	}
	if err := o.Registry.PushBlob(ctx, o.Repository,
		Blob{Digest: layer.Digest, MediaType: layer.MediaType, Content: content}); err != nil {
		return err
	}
	result.Uploaded++
	return nil
}
