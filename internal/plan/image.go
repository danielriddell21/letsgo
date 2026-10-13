package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plugin"
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

	// Plugin says the release image comes from the images hook, so core
	// builds none of its own. Extra images are still core's.
	Plugin bool

	// Extra are the `image <name> ( ... )` blocks: more images of the same
	// binaries, pushed to the same repositories under suffixed tags.
	Extra []ExtraImage
}

// ExtraImage is one more image of the release's binaries, set up its own way.
type ExtraImage struct {
	Name   string
	Suffix string

	// Base, Cmd and Expose are the block's own: nothing is inherited from
	// the release image.
	Base   oci.Reference
	Cmd    []string
	Expose []string
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

	reference, source := p.Config.Image.Reference, config.FileName
	if reference == "" {
		var ok bool
		if reference, ok = p.defaultImageReference(); !ok {
			p.addAt(p.posOf("image"), "image", Fail,
				"no 'origin' remote, so there is no default image name; write one after `image`")
			return
		}
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
		Platforms: p.linuxTargets(),
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

	if p.Config.Image.Plugin && !p.checkImagePlugin(target) {
		return
	}
	if !p.resolveExtraImages(target) {
		return
	}

	p.Image = target
	p.note("image", ref.Registry+"/"+ref.Repository, source)
	p.reportImages(ctx, reference, target)
}

// reportImages adds the image check for every image core builds, and for a
// release image the images hook builds instead.
func (p *Plan) reportImages(ctx context.Context, reference string, target *ImageTarget) {
	if target.Plugin {
		p.add("image", Pass, "%s: the release image comes from the images hook", reference)
	} else {
		p.reportBase(ctx, reference, target)
	}
	for _, extra := range target.Extra {
		p.reportBase(ctx, fmt.Sprintf("%s (%s, tagged *%s)", reference, extra.Name, extra.Suffix),
			&ImageTarget{Base: extra.Base, Platforms: target.Platforms})
	}
}

// linuxTargets are the targets that get an image: nothing else runs in a
// container.
func (p *Plan) linuxTargets() []gobuild.Target {
	var out []gobuild.Target
	for _, t := range p.Targets {
		if t.OS == "linux" {
			out = append(out, t)
		}
	}
	return out
}

// reportBase is the image check for one image: a pass on scratch, or what
// resolving its base said.
func (p *Plan) reportBase(ctx context.Context, reference string, target *ImageTarget) {
	if target.Base == (oci.Reference{}) {
		p.add("image", Pass, "%s on scratch, for %d platform(s)", reference, len(target.Platforms))
		return
	}
	p.checkBase(ctx, reference, target)
}

// checkImagePlugin settles `image plugin`: the images hook builds the
// release image, so it must be pinned, and the settings only core's own
// release image reads would be ignored, which is worth refusing rather than
// letting someone believe they apply.
func (p *Plan) checkImagePlugin(target *ImageTarget) bool {
	if _, ok := p.Plugins[plugin.HookImages]; !ok {
		p.addAt(p.posOf("image"), "image", Fail,
			"image plugin says the release image comes from the images hook, but no plugin is pinned to it; "+
				"pin one, such as letsgo-docker")
		return false
	}
	cfg := p.Config.Image
	if cfg.Base != "" || cfg.Cmd != nil || cfg.Expose != nil {
		p.addAt(p.posOf("image"), "image", Fail,
			"image plugin builds the release image elsewhere, so image base, cmd and expose would not apply; "+
				"set them in the plugin's own config, or in an image block")
		return false
	}
	target.Plugin = true
	return true
}

// resolveExtraImages parses each image block's base.
func (p *Plan) resolveExtraImages(target *ImageTarget) bool {
	for _, extra := range p.Config.Image.Extra {
		resolved := ExtraImage{Name: extra.Name, Suffix: extra.Suffix, Cmd: extra.Cmd, Expose: extra.Expose}
		if tag, err := oci.Tag(p.Version); err == nil && len(tag+extra.Suffix) > oci.MaxTagLength {
			p.addAt(p.configPos(extra.Pos), "image", Fail,
				"image %s: its suffix makes the tag %s%s, longer than the %d characters a registry accepts",
				extra.Name, tag, extra.Suffix, oci.MaxTagLength)
			return false
		}
		if extra.Base != "" {
			parsed, err := oci.ParseReference(extra.Base)
			if err != nil {
				p.addAt(p.configPos(extra.Pos), "image", Fail, "image %s base: %v", extra.Name, err)
				return false
			}
			resolved.Base = parsed
		}
		target.Extra = append(target.Extra, resolved)
	}
	return true
}

// defaultImageReference is the image name nobody has to be told: ghcr.io
// mirrors the repository it is released from. There is none without one.
func (p *Plan) defaultImageReference() (string, bool) {
	if !p.HasRepo() {
		return "", false
	}
	return "ghcr.io/" + p.Repo.Owner + "/" + p.Project, true
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
