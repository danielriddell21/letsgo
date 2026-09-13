package verify

import (
	"context"
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/oci"
)

// checkImages asks whether each published tag still points at the image the
// release recorded.
//
// This is the one question a mutable tag cannot answer about itself. A release
// that recorded an index digest can be checked years later: if `1.2.3` now
// resolves to something else, someone re-pushed over it, and that is worth
// knowing whether it was deliberate or not.
func checkImages(ctx context.Context, result *Result, m *manifest.Manifest) {
	if len(m.Images) == 0 {
		return
	}

	var problems, checked, unreadable []string

	for _, image := range m.Images {
		tag := imageTag(image)
		ref, err := oci.ParseReference(image.Reference + ":" + tag)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", image.Reference, err))
			continue
		}

		// Anonymous: a published image is normally public, and a private one
		// would need a credential letsgo was never given.
		registry := oci.NewRegistry(ref.APIHost())
		registry.UserAgent = result.userAgent

		fetched, err := registry.Manifest(ctx, ref.Repository, tag)
		if err != nil {
			// Unreachable is not the same as wrong. A private package, or a
			// registry that is down, says nothing about the release — but the
			// other images still get checked.
			unreadable = append(unreadable, fmt.Sprintf("%s could not be read: %v", ref, err))
			continue
		}

		if string(fetched.Digest) == image.Digest {
			checked = append(checked, fmt.Sprintf("%s is %s", ref, fetched.Digest.Short()))
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s is now %s, but the release published %s",
			ref, fetched.Digest.Short(), oci.Digest(image.Digest).Short()))
	}

	switch {
	case len(problems) > 0:
		result.add("images", Fail, "%s", strings.Join(append(problems, unreadable...), "\n"))
	case len(unreadable) > 0:
		result.add("images", Warn, "%s", strings.Join(append(checked, unreadable...), "\n"))
	default:
		result.add("images", Pass, "%s", strings.Join(checked, "\n"))
	}
}

// imageTag picks the tag to check: the version, which is the one tag that is
// supposed to be immutable. Checking `latest` would report a failure every
// time a newer release moved it, which is not a failure.
func imageTag(image manifest.Image) string {
	for _, tag := range image.Tags {
		if tag != "latest" {
			return tag
		}
	}
	if len(image.Tags) > 0 {
		return image.Tags[0]
	}
	return "latest"
}
