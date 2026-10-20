package release

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
	pub "github.com/danielriddell21/letsgo/plugin"
)

// SourcePlugin marks an image an images-hook plugin built, in ImageBuild and
// in the manifest. Core's own images carry no source.
const SourcePlugin = "plugin"

// imagesHookDir is where the hook works, under the release's output: the
// staged binaries it builds from, and the layouts it writes.
const imagesHookDir = "images-hook"

// maxTag is the longest tag a registry accepts.
const maxTag = oci.MaxTagLength

// buildPluginImages asks the images hook for the images it builds, and
// prepares each for pushing under the release's tags with its suffix.
//
// The hook runs here, with the rest of the build, so a failed Dockerfile
// stops the release before anything is public, and every digest is fixed
// before the manifest records it.
func buildPluginImages(ctx context.Context, p *plan.Plan, artifacts []build.Artifact, dir string) ([]ImageBuild, error) {
	if p.ImagesHook == nil {
		return nil, nil
	}
	configured := p.Plugins[plugin.HookImages]

	work, err := filepath.Abs(filepath.Join(dir, imagesHookDir))
	if err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	if err := os.RemoveAll(work); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	contextDir, output := filepath.Join(work, "context"), filepath.Join(work, "out")
	if err := os.MkdirAll(output, 0o750); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}

	binaries, err := stageBinaries(p, artifacts, contextDir)
	if err != nil {
		return nil, err
	}

	in := plugin.ImagesInput{
		Project:   p.Project,
		Version:   p.Version,
		Tag:       p.Tag,
		Commit:    p.Git.Commit,
		Date:      p.Git.CommitTime.UTC().Format(time.RFC3339),
		Context:   contextDir,
		Output:    output,
		Binaries:  binaries,
		ConfigDir: filepath.Join(p.RootDir, ".letsgo"),
	}
	var out plugin.ImagesOutput
	if err := plugin.RunWithin(ctx, p.ImagesHook.Timeout, configured, p.RootDir, p.PluginsDir(), in, &out); err != nil {
		return nil, err
	}

	tags, floating, err := imageTags(ctx, p)
	if err != nil {
		return nil, err
	}
	annotations := oci.Annotations(
		"https://"+p.Repo.String(), p.Git.Commit, p.Version, p.Project, "", p.Git.CommitTime)

	layouts, err := checkAnswer(p, out.Images, output, append(append([]string{}, tags...), floating...))
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", configured.Command, err)
	}

	target := p.ImagesHook.Target
	builds := make([]ImageBuild, 0, len(out.Images))

	for i, image := range out.Images {
		layout, err := oci.LoadLayout(layouts[i])
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", configured.Command, err)
		}
		index, err := layout.Index(annotations)
		if err != nil {
			return nil, err
		}

		builds = append(builds, ImageBuild{
			Registry:   target.Registry,
			APIHost:    target.APIHost,
			Repository: target.Repository,
			Version:    p.Version,
			Tags:       suffixed(tags, image.Suffix),
			Floating:   suffixed(floating, image.Suffix),
			Index:      index,
			Layout:     layout,
			Source:     SourcePlugin,
		})
	}
	return builds, nil
}

// stageBinaries copies the release's Linux binaries into the build context,
// as <os>_<arch>/<binary>, so a Dockerfile copies the bytes that were
// published rather than compiling its own.
//
// Only the release's own build is staged. A variant builds the same binary
// for the same platform differently, and one path cannot hold both.
func stageBinaries(p *plan.Plan, artifacts []build.Artifact, contextDir string) ([]plugin.ImageBinary, error) {
	if err := os.MkdirAll(contextDir, 0o750); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}

	var staged []plugin.ImageBinary
	for _, a := range artifacts {
		if a.OS != "linux" || groupFor(p, a).Variant != "" {
			continue
		}
		for _, b := range a.Binaries {
			rel := filepath.ToSlash(filepath.Join(a.OS+"_"+a.Arch, b.Name))
			dst := filepath.Join(contextDir, filepath.FromSlash(rel))
			if _, err := os.Stat(dst); err == nil {
				return nil, fmt.Errorf("release: %s is built twice for %s/%s; the images hook can stage only one",
					b.Name, a.OS, a.Arch)
			}
			if err := copyExecutable(a.Paths[b.Name], dst); err != nil {
				return nil, err
			}
			staged = append(staged, plugin.ImageBinary{Name: b.Name, OS: a.OS, Arch: a.Arch, Path: rel})
		}
	}
	return staged, nil
}

func copyExecutable(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("release: staging %s: %w", src, err)
	}
	// Executable, because a Dockerfile copies it as it is, mode included.
	if err := os.WriteFile(dst, data, 0o755); err != nil { //nolint:gosec // a binary must be executable
		return fmt.Errorf("release: staging %s: %w", src, err)
	}
	return nil
}

// checkAnswer checks the plugin's whole answer before any layout is read: a
// bad suffix or path is the plugin's mistake, and says so whatever it built.
// It returns each image's layout, resolved.
func checkAnswer(p *plan.Plan, images []plugin.OutputImage, output string, tags []string) ([]string, error) {
	if err := checkReleaseImage(p, images); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if p.Image != nil {
		for _, extra := range p.Image.Extra {
			seen[extra.Suffix] = true
		}
	}
	layouts := make([]string, len(images))
	for i, image := range images {
		if err := checkSuffix(image.Suffix, seen, tags); err != nil {
			return nil, err
		}
		layout, err := within(output, image.Layout)
		if err != nil {
			return nil, err
		}
		layouts[i] = layout
	}
	return layouts, nil
}

// checkReleaseImage holds an unsuffixed image, which is the release image
// itself, to what letsgo.mod says builds the release image.
//
// `image plugin` hands it to the hook, which must then build exactly one. No
// `image` directive at all means core builds none either, so the hook may.
// Otherwise core builds it, and a second would overwrite the first's tags.
func checkReleaseImage(p *plan.Plan, images []plugin.OutputImage) error {
	unsuffixed := 0
	for _, image := range images {
		if image.Suffix == "" {
			unsuffixed++
		}
	}

	switch {
	case unsuffixed > 1:
		return fmt.Errorf("%d images have no suffix, and only one can be the release image", unsuffixed)
	case p.Image != nil && p.Image.Plugin && unsuffixed == 0:
		return fmt.Errorf("image plugin says the release image comes from this plugin, but every image it built " +
			"has a suffix; leave the suffix off the release image")
	case p.Image != nil && !p.Image.Plugin && unsuffixed == 1:
		return fmt.Errorf("an image with no suffix would be the release image, which letsgo builds itself here; " +
			"add `image plugin` to letsgo.mod to build it with the plugin instead, or give the image a suffix")
	}
	return nil
}

// checkSuffix refuses a suffix that is repeated, or would make a tag a
// registry rejects. An empty suffix is the release image, which
// checkReleaseImage has already allowed or refused.
func checkSuffix(suffix string, seen map[string]bool, tags []string) error {
	if suffix != "" {
		// The same rule letsgo.mod's image blocks are held to.
		if err := pub.CheckSuffix(suffix); err != nil {
			return err
		}
	}
	if seen[suffix] {
		return fmt.Errorf("suffix %q is already used by another image", suffix)
	}
	seen[suffix] = true

	for _, tag := range tags {
		if len(tag+suffix) > maxTag {
			return fmt.Errorf("suffix %q makes the tag %s%s longer than %d characters", suffix, tag, suffix, maxTag)
		}
	}
	return nil
}

// within resolves a layout path the plugin returned, and refuses one outside
// the output directory core gave it: the plugin decides what is pushed, not
// where on this machine it may be read from.
func within(output, layout string) (string, error) {
	root, err := filepath.EvalSymlinks(output)
	if err != nil {
		return "", fmt.Errorf("output directory: %w", err)
	}
	if !filepath.IsAbs(layout) {
		layout = filepath.Join(output, layout)
	}
	resolved, err := filepath.EvalSymlinks(layout)
	if err != nil {
		return "", fmt.Errorf("layout %s: %w", layout, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("layout %s is not inside %s", layout, output)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("layout %s: %w", layout, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("layout %s is not a directory", layout)
	}
	return resolved, nil
}

func suffixed(tags []string, suffix string) []string {
	if tags == nil {
		return nil
	}
	out := make([]string, len(tags))
	for i, tag := range tags {
		out[i] = tag + suffix
	}
	return out
}
