package plan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/safeexec"
	pub "github.com/danielriddell21/letsgo/plugin"
)

const (
	// DefaultImagesTimeout is how long the images hook may run when its
	// config says nothing. A Dockerfile build is not a question answered from
	// data in hand, so the one-minute limit every other hook has would fail
	// almost any real build.
	DefaultImagesTimeout = 30 * time.Minute

	// MaxImagesTimeout caps what the config may ask for. The Plugin's config
	// sets its own limit, so without a cap a Plugin would be choosing how long
	// it may hold a release.
	MaxImagesTimeout = 2 * time.Hour
)

// ImagesHook is what the images hook needs settled before it runs: where its
// images go, and how long it may take to build them.
type ImagesHook struct {
	// Target names the registry and repository. Only those fields are set:
	// the Plugin builds the images, so there is no base, command or platform
	// for core to decide.
	Target *ImageTarget

	Timeout time.Duration
}

// resolveImagesHook settles a pinned images hook: its timeout, where its
// images are pushed, and whether this machine can build them at all.
//
// The Plugin's config is read here only for what core itself enforces, which
// is the timeout, and for the platforms, which only earn a warning. Every
// other setting is the Plugin's to check when it runs. A Plugin with no
// config at all is fine: letsgo-docker builds ./Dockerfile without one.
func (p *Plan) resolveImagesHook(ctx context.Context) {
	configured, ok := p.Plugins[plugin.HookImages]
	if !ok {
		return
	}
	pos := p.posOf(pluginDirective + string(plugin.HookImages))

	hook := &ImagesHook{Timeout: DefaultImagesTimeout}
	timeoutFrom := "the default"

	cfg, err := pub.LoadConfig(filepath.Join(p.RootDir, pluginConfigDir), plugin.ShortName(configured.Command))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.warnRunnerPlatform(nil)
	case err != nil:
		p.addAt(pos, "images", Fail, "%v", err)
		return
	default:
		if !p.readImagesConfig(cfg, hook) {
			return
		}
		timeoutFrom = cfg.Path
	}

	hook.Target = p.imagesHookTarget(pos)
	if hook.Target == nil {
		return
	}

	if known, ok := plugin.Lookup(configured.Command); ok && len(known.Probe) > 0 {
		if err := probe(ctx, known.Probe, known.ProbeEnv); err != nil {
			p.addAt(pos, "images", Fail, "%s needs `%s` to work, and it does not here: %v",
				configured.Command, strings.Join(known.Probe, " "), err)
			return
		}
	}

	p.ImagesHook = hook
	p.note("images hook timeout", hook.Timeout.String(), timeoutFrom)
	p.add("images", Pass, "%s builds into %s/%s, within %s",
		configured.Command, hook.Target.Registry, hook.Target.Repository, hook.Timeout)
}

// readImagesConfig takes the timeout from the Plugin's config, and warns about
// any image built for a platform the release does not build. It reports
// whether the plan can go on.
func (p *Plan) readImagesConfig(cfg *pub.Config, hook *ImagesHook) bool {
	at := func(l pub.ConfigLine) *Pos {
		return &Pos{File: cfg.Path, Line: l.Pos.Line, Col: l.Pos.Col}
	}

	targets := p.linuxTargets()
	linux := make([]string, 0, len(targets))
	for _, t := range targets {
		linux = append(linux, t.String())
	}

	seenTimeout := false
	for _, l := range cfg.Lines {
		switch {
		case l.Keyword == "timeout":
			if seenTimeout {
				p.addAt(at(l), "images", Fail, "timeout is already set")
				return false
			}
			seenTimeout = true

			d, problem := imagesTimeout(l.Args)
			if problem != "" {
				p.addAt(at(l), "images", Fail, "%s", problem)
				return false
			}
			hook.Timeout = d

		case l.Keyword == "image" && len(l.Args) >= 2 && l.Args[1] == "platform":
			for _, platform := range l.Args[2:] {
				if !slices.Contains(linux, platform) {
					p.addAt(at(l), "images", Warn,
						"image %s builds for %s, which this release does not build, so no binary is staged for it",
						l.Args[0], platform)
				}
			}
		}
	}

	p.warnRunnerPlatform(cfg)
	return true
}

// warnRunnerPlatform says, once, when an image will build for this machine's
// platform and the release does not build it, so no binary is staged for it.
// An image naming no platform does, and so does the one image a config naming
// no images at all, or no config, leaves letsgo-docker to build.
func (p *Plan) warnRunnerPlatform(cfg *pub.Config) {
	targets := p.linuxTargets()
	linux := make([]string, 0, len(targets))
	for _, t := range targets {
		linux = append(linux, t.String())
	}
	runner := "linux/" + runtime.GOARCH
	if len(linux) == 0 || slices.Contains(linux, runner) || !onRunnerPlatform(cfg) {
		return
	}
	p.add("images", Warn,
		"an image with no platform builds for this machine's %s, which this release does not build, "+
			"so no binary is staged for it", runner)
}

// imagesTimeout reads a timeout line's arguments, or says what is wrong with
// them.
func imagesTimeout(args []string) (time.Duration, string) {
	if len(args) != 1 {
		return 0, "expected `timeout <duration>`, such as `timeout 45m`"
	}
	d, err := time.ParseDuration(args[0])
	if err != nil || d <= 0 {
		return 0, fmt.Sprintf("timeout %q is not a duration, such as 45m", args[0])
	}
	if d > MaxImagesTimeout {
		return 0, fmt.Sprintf("timeout %s is more than the %s letsgo allows", d, MaxImagesTimeout)
	}
	return d, ""
}

// onRunnerPlatform reports whether any image builds for the runner's
// platform: one whose block leaves its platform to the default, or the
// default image of a config, nil or not, that names none.
func onRunnerPlatform(cfg *pub.Config) bool {
	if cfg == nil {
		return true
	}
	named, platformed := map[string]bool{}, map[string]bool{}
	for _, l := range cfg.Lines {
		if l.Keyword != "image" || len(l.Args) < 2 {
			continue
		}
		named[l.Args[0]] = true
		if l.Args[1] == "platform" {
			platformed[l.Args[0]] = true
		}
	}
	if len(named) == 0 {
		return true
	}
	for name := range named {
		if !platformed[name] {
			return true
		}
	}
	return false
}

// imagesHookTarget is where the hook's images are pushed: wherever the
// release's own image goes, or, with no `image` directive, the default
// reference that directive would have used, without building an image of
// core's own.
func (p *Plan) imagesHookTarget(pos *Pos) *ImageTarget {
	if p.Image != nil {
		return &ImageTarget{Registry: p.Image.Registry, APIHost: p.Image.APIHost, Repository: p.Image.Repository}
	}
	if p.Config.Image != nil {
		// An `image` directive that failed to resolve has already said why.
		return nil
	}

	reference, ok := p.defaultImageReference()
	if !ok {
		p.addAt(pos, "images", Fail,
			"no 'origin' remote, so there is nowhere to push the images; add `image <reference>` to %s",
			config.FileName)
		return nil
	}
	ref, err := oci.ParseReference(reference)
	if err != nil {
		p.addAt(pos, "images", Fail, "%v", err)
		return nil
	}
	p.note("image", ref.Registry+"/"+ref.Repository, "the repository owner and project name")
	return &ImageTarget{Registry: ref.Registry, APIHost: ref.APIHost(), Repository: ref.Repository}
}

// probe checks that a tool a known plugin needs answers at all.
//
// The program is looked for where the plugin itself looks: an absolute path
// in envVar, else the system directories. Probing anywhere else could pass a
// plan the plugin then fails, or run a program nobody chose.
func probe(ctx context.Context, command []string, envVar string) error {
	bin, err := probeProgram(command[0], envVar)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, command[1:]...)
	cmd.Env = safeexec.EnvWithFixedPath(os.Environ(), filepath.Dir(bin))
	out, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return fmt.Errorf("%s: %w", bin, err)
	}
	return nil
}

// probeProgram finds a probe's program without consulting PATH.
func probeProgram(name, envVar string) (string, error) {
	if envVar != "" {
		if value := os.Getenv(envVar); value != "" {
			return safeexec.Override(name, value, envVar)
		}
	}
	bin, err := safeexec.LookIn(safeexec.SystemDirs(), safeexec.Exe(name))
	if err != nil && envVar != "" {
		return "", fmt.Errorf("%w; set %s to its absolute path", err, envVar)
	}
	return bin, err
}
