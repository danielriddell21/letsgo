package release

import (
	"context"
	"fmt"
	"sort"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/semver"
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

	// Version is the plain semver this image was built for, kept alongside
	// Tags so a push can compare it against whatever a floating tag
	// currently points at.
	Version string

	// Tags are pushed unconditionally: the version tag, and a prerelease's
	// channel tag.
	Tags []string

	// Floating are pushed only if Version is newer than what the tag
	// currently points at: a stable release's major.minor, major, latest,
	// and every channel this module has ever published a prerelease under.
	Floating []string

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

	tags, floating, err := imageTags(ctx, p)
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
			Version:    p.Version,
			Tags:       tags,
			Floating:   floating,
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

// imageTags is what a release targets the index under: tags that are always
// pushed, and floating tags that move only when this release is newer than
// what they currently point at (internal/release/push.go decides that; this
// only computes the candidates, from the version and, for a stable release,
// this module's own channel history — never the registry, so the same commit
// always plans the same tags regardless of what has or hasn't shipped yet).
func imageTags(ctx context.Context, p *plan.Plan) (tags, floating []string, err error) {
	version, err := oci.Tag(p.Version)
	if err != nil {
		return nil, nil, err
	}
	v, ok := semver.Parse(p.Version)
	if !ok {
		return nil, nil, fmt.Errorf("release: %q is not a version image tags can be derived from", p.Version)
	}

	if v.IsPrerelease() {
		channel, _ := oci.Channel(p.Version)
		return []string{version, channel}, nil, nil
	}

	tags = []string{version}
	if p.Snapshot {
		return tags, nil, nil
	}

	channels, err := channelHistory(ctx, p)
	if err != nil {
		return nil, nil, err
	}

	major := fmt.Sprintf("%d", v.Major)
	floating = make([]string, 0, 3+len(channels))
	floating = append(floating, fmt.Sprintf("%s.%d", major, v.Minor), major, "latest")
	return tags, append(floating, channels...), nil
}

// channelHistory finds every channel this module has ever published a
// prerelease under, from its own tags: the set a stable release must
// consider advancing (a channel that has ever existed keeps being tracked,
// so a channel's followers move to stable once it's newer).
func channelHistory(ctx context.Context, p *plan.Plan) ([]string, error) {
	tags, err := discover.Tags(ctx, p.GitBin, p.RootDir, p.Scope.Prefix)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var channels []string
	for _, tag := range tags {
		rest, ok := p.Scope.MatchesTag(tag)
		if !ok {
			continue
		}
		channel, ok := oci.Channel(rest)
		if !ok || seen[channel] {
			continue
		}
		seen[channel] = true
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	return channels, nil
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
			Tags:      append(append([]string{}, b.Tags...), b.Floating...),
			Platforms: platforms,
		}
		if b.Base != nil {
			// The digest, not the tag: a tag says what was asked for, and a
			// digest says what was used. Substituted rather than appended,
			// because a base the config already pinned by digest would
			// otherwise be recorded carrying two of them.
			record.Base = b.Base.Reference.WithDigest(b.Base.IndexDigest).String()
		}
		out = append(out, record)
	}
	return out
}
