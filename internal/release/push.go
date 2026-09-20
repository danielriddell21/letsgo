package release

import (
	"context"
	"fmt"
	"os"

	"github.com/danielriddell21/letsgo/internal/oci"
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
func PushImages(ctx context.Context, builds []ImageBuild, token string, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	for _, built := range builds {
		registry := registryFor(built.APIHost, token)

		result, err := oci.Push(ctx, oci.PushOptions{
			Registry:   registry,
			Repository: built.Repository,
			Tags:       built.Tags,
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
func baseSource(built ImageBuild, target *oci.Registry) *oci.Source {
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

// Describe renders what was assembled, for a build that is not publishing.
func Describe(builds []ImageBuild) string {
	if len(builds) == 0 {
		return ""
	}
	out := ""
	for _, b := range builds {
		out += fmt.Sprintf("  %s  %s\n", b.Reference(), b.Index.Digest)
	}
	return out
}
