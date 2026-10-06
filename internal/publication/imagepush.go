package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/danielriddell21/letsgo/internal/release"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// registryEnv names the variables consulted for registry credentials on a
// host that is not ghcr.io. Two names rather than one because CI systems are
// already set up for whichever they use.
var registryEnv = [][2]string{
	{"REGISTRY_USERNAME", "REGISTRY_PASSWORD"},
	{"DOCKER_USERNAME", "DOCKER_PASSWORD"},
}

// PushImages publishes every assembled image under every tag it carries.
//
// Each tag is a separate write of the same index, which costs one request and
// means `latest` names exactly the bytes the version tag names rather than a
// second build that happens to be equal.
func PushImages(ctx context.Context, builds []release.ImageBuild, token string, logf func(string, ...any)) error {
	return pushImages(ctx, builds, func(host string) *oci.Registry { return registryFor(host, token) }, logf)
}

// pushImages is PushImages with the registry client for a host supplied, so
// it can be driven against an in-process registry.
func pushImages(ctx context.Context, builds []release.ImageBuild, registryAt func(host string) *oci.Registry, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	for _, built := range builds {
		registry := registryAt(built.APIHost)

		tags, err := resolveFloatingTags(ctx, registry, built, logf)
		if err != nil {
			return err
		}

		result, err := oci.Push(ctx, oci.PushOptions{
			Registry:   registry,
			Repository: built.Repository,
			Tags:       tags,
			Images:     built.Images,
			Index:      built.Index,
			Base:       baseSource(built, registry),
		})
		if err != nil {
			return err
		}

		logf("pushed %s (%s) for %v, %d blobs uploaded and %d already present",
			built.Reference(), result.Digest.Short(), result.Platforms,
			result.Uploaded, result.Skipped)
	}
	return nil
}

// resolveFloatingTags starts from the tags a build always pushes, and adds
// each floating tag that's newer than what it currently points at, or that
// has never been pushed. Floating tags only move forward: a backport moves
// its major.minor, but never a major or latest that's already ahead of it.
func resolveFloatingTags(ctx context.Context, registry *oci.Registry, built release.ImageBuild, logf func(string, ...any)) ([]string, error) {
	tags := append([]string{}, built.Tags...)
	if len(built.Floating) == 0 {
		return tags, nil
	}

	releasing, ok := semver.Parse(built.Version)
	if !ok {
		return nil, fmt.Errorf("release: %q is not a version floating tags can be compared against", built.Version)
	}

	for _, tag := range built.Floating {
		newer, err := newerThanCurrent(ctx, registry, built.Repository, tag, releasing, logf)
		if err != nil {
			return nil, err
		}
		if newer {
			tags = append(tags, tag)
		} else {
			logf("kept %s at its current target: %s is not newer", tag, built.Version)
		}
	}
	return tags, nil
}

// newerThanCurrent reports whether releasing is newer than whatever tag
// currently points at. A tag that has never been pushed always counts as
// newer, since there is nothing yet for releasing to be older than.
func newerThanCurrent(
	ctx context.Context, registry *oci.Registry, repo, tag string, releasing semver.Version, logf func(string, ...any),
) (bool, error) {
	fetched, err := registry.Manifest(ctx, repo, tag)
	if err != nil {
		var apiErr *oci.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return true, nil
		}
		return false, err
	}

	current, ok := currentVersion(fetched.Content)
	if !ok {
		logf("%s/%s:%s has no readable version annotation, leaving it alone", registry.Host, repo, tag)
		return false, nil
	}
	return semver.Compare(releasing, current) > 0, nil
}

// currentVersion reads the version a manifest or index was built for, from
// the same annotation oci.Annotations sets on every one letsgo pushes.
func currentVersion(content []byte) (semver.Version, bool) {
	var doc struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return semver.Version{}, false
	}
	return semver.Parse(doc.Annotations["org.opencontainers.image.version"])
}

// registryFor builds an authenticated client for a host.
func registryFor(host, token string) *oci.Registry {
	registry := oci.NewRegistry(host)

	if host == "ghcr.io" {
		// ghcr accepts the forge token directly, and ignores the username, so
		// a repository that can publish a release can publish its image with
		// no second credential to configure.
		registry.Username, registry.Password = "letsgo", token
		return registry
	}

	for _, pair := range registryEnv {
		if user, pass := os.Getenv(pair[0]), os.Getenv(pair[1]); user != "" || pass != "" {
			registry.Username, registry.Password = user, pass
			return registry
		}
	}
	return registry
}

// baseSource says where a base image's layers can be fetched from, which is
// needed only when there is a base.
func baseSource(built release.ImageBuild, target *oci.Registry) *oci.Source {
	if built.Base == nil {
		return nil
	}
	ref := built.Base.Reference

	source := &oci.Source{Repository: ref.Repository}
	if ref.APIHost() == target.Host {
		// Same host: reuse the authenticated client so a cross-repository
		// mount is attempted with credentials that permit it.
		source.Registry = target
		return source
	}
	source.Registry = oci.NewRegistry(ref.APIHost())
	return source
}
