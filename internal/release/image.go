package release

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// ImageBuild is one repository's assembled, unpushed image.
//
// Assembly happens during the build rather than at publish time so that the
// digest is known before anything is written anywhere. That is what lets the
// manifest record it, and it means `letsgo build` can tell you exactly what
// image a release would produce without producing one.
type ImageBuild struct {
	// Registry is the host as published; APIHost is where the client talks.
	// They differ only for Docker Hub, which is exactly the case that would
	// otherwise put "registry-1.docker.io" into a release manifest.
	Registry   string
	APIHost    string
	Repository string
	Tags       []string

	Images []*oci.Image
	Index  oci.Blob
	Base   *oci.Base
}

// Reference is the image's primary name.
func (b ImageBuild) Reference() string {
	return fmt.Sprintf("%s/%s:%s", b.Registry, b.Repository, b.Tags[0])
}

// buildImages assembles one image per command, for every Linux target.
func buildImages(ctx context.Context, p *plan.Plan, artifacts []build.Artifact, warnf func(string, ...any)) ([]ImageBuild, error) {
	if p.Image == nil {
		return nil, nil
	}

	binaries, byBinary := groupForImages(p, artifacts)
	if len(binaries) == 0 {
		return nil, fmt.Errorf("release: an image was asked for but no linux binary was built")
	}

	tags, err := imageTags(p)
	if err != nil {
		return nil, err
	}

	repos := p.Image.Repos(binaries)
	annotations := oci.Annotations(
		"https://"+p.Repo.String(), p.Git.Commit, p.Version, p.Project, "", p.Git.CommitTime)

	cache := bases{}
	out := make([]ImageBuild, 0, len(binaries))

	for _, binary := range binaries {
		built := ImageBuild{
			Registry:   p.Image.Registry,
			APIHost:    p.Image.APIHost,
			Repository: repos[binary],
			Tags:       tags,
		}

		if err := buildOne(ctx, p, &built, binary, byBinary[binary], annotations, cache, warnf); err != nil {
			return nil, err
		}
		out = append(out, built)
	}
	return out, nil
}

// groupForImages collects the artifacts that get an image, keyed by the binary
// they carry and in the order the commands were built.
//
// The grouping is the point: a module with several commands produces several
// images rather than one that silently contains the last binary compiled.
func groupForImages(p *plan.Plan, artifacts []build.Artifact) ([]string, map[string][]build.Artifact) {
	platforms := make(map[string]bool, len(p.Image.Platforms))
	for _, t := range p.Image.Platforms {
		platforms[t.String()] = true
	}

	var binaries []string
	byBinary := map[string][]build.Artifact{}

	// An image holds one program, so an archive carrying several produces
	// several images rather than one image with a choice of entrypoints.
	for _, a := range artifacts {
		if !platforms[a.Target] {
			continue
		}
		for _, b := range a.Binaries {
			if _, seen := byBinary[b.Name]; !seen {
				binaries = append(binaries, b.Name)
			}
			byBinary[b.Name] = append(byBinary[b.Name], a)
		}
	}
	return binaries, byBinary
}

// buildOne assembles every platform's image for one binary, and the index that
// ties them together.
func buildOne(
	ctx context.Context,
	p *plan.Plan,
	built *ImageBuild,
	binary string,
	artifacts []build.Artifact,
	annotations map[string]string,
	cache bases,
	warnf func(string, ...any),
) error {
	for _, a := range artifacts {
		platform := oci.Platform{OS: a.OS, Architecture: a.Arch}

		base, err := cache.resolve(ctx, p, platform, warnf)
		if err != nil {
			return err
		}
		built.Base = base

		image, err := oci.BuildImage(oci.ImageOptions{
			Binary:       a.Paths[binary],
			Name:         binary,
			Platform:     platform,
			Created:      p.Git.CommitTime,
			Base:         base,
			Cmd:          p.Image.Cmd,
			ExposedPorts: p.Image.Expose,
			Annotations:  annotations,
		})
		if err != nil {
			return err
		}
		built.Images = append(built.Images, image)
	}

	index, err := oci.BuildIndex(built.Images, annotations)
	if err != nil {
		return err
	}
	built.Index = index
	return nil
}

// bases resolves each platform's base image once. A module with three
// commands would otherwise read the same base three times per platform.
type bases map[string]*oci.Base

// resolve reads the base image for one platform, or returns nil for scratch.
func (cache bases) resolve(ctx context.Context, p *plan.Plan, platform oci.Platform, warnf func(string, ...any)) (*oci.Base, error) {
	ref := p.Image.Base
	if ref == (oci.Reference{}) {
		return nil, nil
	}

	key := platform.String()
	if cached, ok := cache[key]; ok {
		return cached, nil
	}

	// Base images are public in every case worth supporting, so this is an
	// anonymous read. A private base would need credentials letsgo has no
	// way to be given yet, and saying so beats failing obscurely.
	registry := oci.NewRegistry(ref.APIHost())
	registry.UserAgent = "letsgo"

	base, err := oci.ResolveBase(ctx, registry, ref, platform)
	if err != nil {
		return nil, err
	}
	if warnf != nil && ref.Digest == "" {
		warnf("base %s resolved to %s for %s", ref, base.Digest.Short(), platform)
	}

	cache[key] = base
	return base, nil
}

// imageTags is what the index is published under: the version always, and
// `latest` for a release people should be getting by default.
func imageTags(p *plan.Plan) ([]string, error) {
	version, err := oci.Tag(p.Version)
	if err != nil {
		return nil, err
	}
	tags := []string{version}
	if !p.Snapshot && !prerelease(p.Version) {
		tags = append(tags, "latest")
	}
	return tags, nil
}

// prerelease reports whether a version carries a prerelease segment, which is
// the one thing `latest` must never point at. Build metadata after "+" is not
// part of that judgement and can itself contain a hyphen.
func prerelease(version string) bool {
	base, _, _ := strings.Cut(version, "+")
	return strings.Contains(base, "-")
}

// imageRecords describes the built images for the release manifest.
func imageRecords(builds []ImageBuild) []manifest.Image {
	if len(builds) == 0 {
		return nil
	}

	out := make([]manifest.Image, 0, len(builds))
	for _, b := range builds {
		platforms := make([]string, 0, len(b.Images))
		for _, img := range b.Images {
			platforms = append(platforms, img.Platform.String())
		}
		sort.Strings(platforms)

		record := manifest.Image{
			Reference: b.Registry + "/" + b.Repository,
			Digest:    string(b.Index.Digest),
			Tags:      b.Tags,
			Platforms: platforms,
		}
		if b.Base != nil {
			// The digest, not the tag: a tag says what was asked for, and a
			// digest says what was used.
			record.Base = b.Base.Reference + "@" + string(b.Base.IndexDigest)
		}
		out = append(out, record)
	}
	return out
}
