package oci

import (
	"fmt"
	"strings"
)

// DefaultRegistry is where a reference with no host is assumed to live, as
// every other container tool assumes.
const DefaultRegistry = "docker.io"

// dockerAPIHost is the host docker.io's registry API actually answers on.
const dockerAPIHost = "registry-1.docker.io"

// Reference names an image.
type Reference struct {
	Registry   string
	Repository string

	// Tag and Digest: at most one is set. A reference with neither means the
	// conventional "latest".
	Tag    string
	Digest Digest
}

// ParseReference reads a reference such as "ghcr.io/you/tool:v1.2.3" or
// "gcr.io/distroless/static@sha256:abc…".
func ParseReference(s string) (Reference, error) {
	text := strings.TrimSpace(s)
	if text == "" {
		return Reference{}, fmt.Errorf("oci: empty image reference")
	}

	var ref Reference

	// The digest is split first: it contains a colon, which the tag split
	// would otherwise claim.
	if name, digest, ok := strings.Cut(text, "@"); ok {
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
			return Reference{}, fmt.Errorf("oci: %q is not a sha256 digest", digest)
		}
		text, ref.Digest = name, Digest(digest)
	}

	// A host is distinguished from a first path element by containing a dot or
	// a port, which is the same rule every other container tool applies.
	name := text
	if first, rest, ok := strings.Cut(text, "/"); ok &&
		(strings.ContainsAny(first, ".:") || first == "localhost") {
		ref.Registry, name = first, rest
	}

	// Only the last path element may carry a tag, so a port in the host is not
	// mistaken for one.
	if _, tag, ok := strings.Cut(lastElement(name), ":"); ok {
		if tag == "" {
			return Reference{}, fmt.Errorf("oci: %q has an empty tag", s)
		}
		name = strings.TrimSuffix(name, ":"+tag)

		// A reference carrying both is what Dependabot and Renovate write when
		// they pin a base image, so it is the spelling anything automating
		// base bumps produces. The digest is what gets fetched and the tag is
		// documentation, so the tag is dropped rather than recorded: Target
		// resolves to exactly one of them, and when both are written it is
		// never the tag.
		if ref.Digest == "" {
			ref.Tag = tag
		}
	}

	if ref.Registry == "" {
		ref.Registry = DefaultRegistry
		// Docker Hub's official images live under an implicit namespace.
		if !strings.Contains(name, "/") {
			name = "library/" + name
		}
	}
	if name == "" {
		return Reference{}, fmt.Errorf("oci: %q names no repository", s)
	}
	ref.Repository = name

	return ref, nil
}

func lastElement(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Target is the tag or digest to address, with "latest" when neither is given.
func (r Reference) Target() string {
	switch {
	case r.Digest != "":
		return string(r.Digest)
	case r.Tag != "":
		return r.Tag
	default:
		return "latest"
	}
}

// APIHost is the host the registry API answers on, which for Docker Hub is
// not the host people write in a reference.
func (r Reference) APIHost() string {
	if r.Registry == DefaultRegistry {
		return dockerAPIHost
	}
	return r.Registry
}

// String renders the reference back.
func (r Reference) String() string {
	out := r.Registry + "/" + r.Repository
	switch {
	case r.Digest != "":
		return out + "@" + string(r.Digest)
	case r.Tag != "":
		return out + ":" + r.Tag
	default:
		return out
	}
}

// WithTag returns the same repository at a different tag.
func (r Reference) WithTag(tag string) Reference {
	r.Tag, r.Digest = tag, ""
	return r
}
