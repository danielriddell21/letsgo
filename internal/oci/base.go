package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Source is a repository that blobs can be read or mounted from.
type Source struct {
	Registry   *Registry
	Repository string
}

// ResolveBase reads a base image for one platform.
//
// The reference may be a tag or a digest, and may point at a multi-platform
// index; what comes back is always one platform's manifest, named by the
// digest of the bytes actually received. Everything downstream then works from
// a pinned digest, whatever was written in the config file.
func ResolveBase(ctx context.Context, reg *Registry, ref Reference, platform Platform) (*Base, error) {
	fetched, err := reg.Manifest(ctx, ref.Repository, ref.Target())
	if err != nil {
		return nil, fmt.Errorf("oci: reading base image %s: %w", ref, err)
	}

	indexDigest := fetched.Digest

	if isIndex(fetched.MediaType, fetched.Content) {
		digest, err := selectPlatform(fetched.Content, platform)
		if err != nil {
			return nil, fmt.Errorf("oci: base image %s: %w", ref, err)
		}
		fetched, err = reg.Manifest(ctx, ref.Repository, string(digest))
		if err != nil {
			return nil, fmt.Errorf("oci: reading base image %s for %s: %w", ref, platform, err)
		}
	}

	var manifest Manifest
	if err := json.Unmarshal(fetched.Content, &manifest); err != nil {
		return nil, fmt.Errorf("oci: parsing base image %s: %w", ref, err)
	}
	if manifest.Config.Digest == "" {
		return nil, fmt.Errorf("oci: base image %s has no config; it may be a legacy schema 1 manifest", ref)
	}

	configBlob, err := reg.Blob(ctx, ref.Repository, manifest.Config.Digest)
	if err != nil {
		return nil, fmt.Errorf("oci: reading base image %s config: %w", ref, err)
	}

	var config Config
	if err := json.Unmarshal(configBlob, &config); err != nil {
		return nil, fmt.Errorf("oci: parsing base image %s config: %w", ref, err)
	}

	return &Base{
		Reference:   ref,
		Digest:      fetched.Digest,
		IndexDigest: indexDigest,
		Config:      config,
		Layers:      manifest.Layers,
	}, nil
}

// isIndex reports whether a fetched document is a manifest list.
//
// The media type is checked first and the body second, because a registry is
// free to answer with a bare "application/json" and leave the caller to work
// it out.
func isIndex(mediaType string, content []byte) bool {
	switch {
	case strings.HasPrefix(mediaType, MediaTypeIndex),
		strings.HasPrefix(mediaType, MediaTypeDockerList):
		return true
	case strings.HasPrefix(mediaType, MediaTypeManifest),
		strings.HasPrefix(mediaType, MediaTypeDockerMani):
		return false
	}

	var probe struct {
		Manifests []json.RawMessage `json:"manifests"`
	}
	return json.Unmarshal(content, &probe) == nil && len(probe.Manifests) > 0
}

// selectPlatform picks one platform's manifest out of an index.
func selectPlatform(content []byte, want Platform) (Digest, error) {
	var index Index
	if err := json.Unmarshal(content, &index); err != nil {
		return "", fmt.Errorf("parsing index: %w", err)
	}

	var available []string
	for _, m := range index.Manifests {
		if m.Platform == nil {
			continue
		}
		// Attestation manifests are listed alongside the real ones with a
		// placeholder platform, and picking one would produce an image that
		// cannot run.
		if m.Platform.OS == "unknown" || m.Platform.Architecture == "unknown" {
			continue
		}
		available = append(available, m.Platform.String())
		if m.Platform.OS == want.OS && m.Platform.Architecture == want.Architecture {
			return m.Digest, nil
		}
	}
	return "", fmt.Errorf("has no %s image (it has %s)", want, strings.Join(available, ", "))
}
