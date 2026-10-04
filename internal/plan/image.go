package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/oci"
)

// ImageTarget is where a release's container images go.
type ImageTarget struct {
	// Registry is the host as it is written and published — "docker.io", not
	// the "registry-1.docker.io" its API answers on. APIHost is the latter.
	// Recording the written form matters: it is what goes in the manifest and
	// what someone types into `docker pull`.
	Registry string
	APIHost  string

	// Repository is the path under the registry. A module with one command
	// publishes to Repository; one with several publishes each command to
	// Repository/<binary>, because a repository holds one image.
	Repository string

	// Base is the image to stack on. The zero value means scratch.
	Base oci.Reference

	// Cmd is the default argument list, and Expose the ports to record. Both
	// are config verbatim: they are strings in the image config, so the commit
	// pins them exactly as it pins the reference.
	Cmd    []string
	Expose []string

	// Platforms are the targets that get an image, which is the Linux subset
	// of the build matrix: nothing else runs in a container.
	Platforms []gobuild.Target
}

// Repos returns the repository each binary publishes to.
func (t *ImageTarget) Repos(binaries []string) map[string]string {
	out := make(map[string]string, len(binaries))
	for _, binary := range binaries {
		if len(binaries) == 1 {
			out[binary] = t.Repository
			continue
		}
		out[binary] = t.Repository + "/" + binary
	}
	return out
}

// resolveImage works out where the container images go.
//
// The reference names a repository and nothing else: the tag comes from the
// release, so accepting one here would create two answers to what a release is
// called and let them disagree.
func (p *Plan) resolveImage(ctx context.Context) {
	if p.Config.Image == nil {
		return
	}

	reference, source := p.Config.Image.Reference, ConfigFile
	if reference == "" {
		if !p.HasRepo {
			p.addAt(p.posOf("image"), "image", Fail,
				"no 'origin' remote, so there is no default image name; write one after `image`")
			return
		}
		// ghcr.io mirrors the repository it is released from, which is the
		// one name nobody has to be told.
		reference = "ghcr.io/" + p.Repo.Owner + "/" + p.Project
		source = "the repository owner and project name"
	}

	ref, err := oci.ParseReference(reference)
	if err != nil {
		p.addAt(p.posOf("image"), "image", Fail, "%v", err)
		return
	}
	if ref.Tag != "" || ref.Digest != "" {
		p.addAt(p.posOf("image"), "image", Fail,
			"%s carries a tag; the tag comes from the release, so name the repository only", reference)
		return
	}

	target := &ImageTarget{
		Registry: ref.Registry, APIHost: ref.APIHost(), Repository: ref.Repository,
		Cmd: p.Config.Image.Cmd, Expose: p.Config.Image.Expose,
	}
	for _, t := range p.Targets {
		if t.OS == "linux" {
			target.Platforms = append(target.Platforms, t)
		}
	}
	if len(target.Platforms) == 0 {
		p.addAt(p.posOf("image"), "image", Fail, "an image was asked for but no linux target is built")
		return
	}

	if base := p.Config.Image.Base; base != "" {
		parsed, err := oci.ParseReference(base)
		if err != nil {
			p.addAt(p.posOf("image"), "image", Fail, "image base: %v", err)
			return
		}
		target.Base = parsed
	}

	p.Image = target
	p.note("image", ref.Registry+"/"+ref.Repository, source)

	if target.Base == (oci.Reference{}) {
		p.add("image", Pass, "%s on scratch, for %d platform(s)", reference, len(target.Platforms))
		return
	}
	p.checkBase(ctx, reference, target)
}

// checkBase resolves the base image while planning rather than while building.
//
// Whether a base can be fetched is a fact about the release, and plan's whole
// purpose is to surface those in two seconds rather than after a cross-compile
// of every target. Resolving it here also turns the "named by tag" warning
// from advice into an instruction: it can name the digest to pin.
//
// One platform is enough to answer the question. The release resolves each
// one and caches them, which is a different job.
func (p *Plan) checkBase(ctx context.Context, reference string, target *ImageTarget) {
	registry := oci.NewRegistry(target.Base.APIHost())
	registry.UserAgent = "letsgo"

	platform := oci.Platform{OS: "linux", Architecture: target.Platforms[0].Arch}

	base, err := oci.ResolveBase(ctx, registry, target.Base, platform)

	status, detail := baseResult(reference, target, base, err)
	p.add("image", status, "%s", detail)
}

// baseResult judges a base resolution.
//
// Separated from the fetch above because the judgement is the part worth
// testing: whether a failure is the config's fault turns on who answered, and
// that distinction should not need a registry to exercise.
func baseResult(reference string, target *ImageTarget, base *oci.Base, err error) (Status, string) {
	where := fmt.Sprintf("%s on %s, for %d platform(s)", reference, target.Base, len(target.Platforms))

	if err != nil {
		// A registry that answered is reporting a real problem with the
		// reference — the wrong repository, a tag that does not exist, a
		// private image. One that could not be reached says nothing about the
		// config, and planning offline is worth keeping.
		var answered *oci.Error
		if errors.As(err, &answered) {
			return Fail, err.Error()
		}
		return Warn, fmt.Sprintf(
			"%s\n  the base could not be resolved, so it was not checked: %v", where, err)
	}

	if target.Base.Digest == "" {
		// A tag is a moving target. The resolved digest is recorded in the
		// manifest either way, so this is a warning rather than a refusal —
		// and naming the digest makes the fix a copy and paste.
		return Warn, fmt.Sprintf(
			"%s\n  the base is named by tag, so two releases of the same commit can differ; "+
				"pin it with @%s", where, base.IndexDigest)
	}
	return Pass, where
}
