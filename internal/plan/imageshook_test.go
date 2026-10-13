package plan_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/plan"
)

const imagesPin = "plugin images letsgo-docker v0.1.0 sha256:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"

// fakeDocker names a docker in LETSGO_DOCKER, as a machine with docker
// outside the system directories would, answering `docker buildx version`
// successfully or not.
func fakeDocker(t *testing.T, works bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\necho 'github.com/docker/buildx v0.17.0'\n"
	if !works {
		body = "#!/bin/sh\necho \"docker: 'buildx' is not a docker command.\" >&2\nexit 1\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LETSGO_DOCKER", filepath.Join(dir, "docker"))
}

// imagesRepo is a one-command module on GitHub pinning the images hook, with
// the given docker.mod (none when empty).
func imagesRepo(t *testing.T, mod, imagesMod string) *plan.Plan {
	t.Helper()
	r := newRepo(t)
	r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+mod+imagesPin)
	if imagesMod != "" {
		r.write(".letsgo/docker.mod", imagesMod)
	}
	r.commit("v1.0.0")
	return r.resolve(plan.Options{})
}

func imagesChecks(p *plan.Plan) []plan.Check {
	var out []plan.Check
	for _, c := range p.Checks {
		if c.Name == "images" {
			out = append(out, c)
		}
	}
	return out
}

func failsWith(t *testing.T, p *plan.Plan, want string) {
	t.Helper()
	if p.OK() {
		t.Fatalf("the plan passed; want a failure mentioning %q", want)
	}
	for _, c := range imagesChecks(p) {
		if c.Status == plan.Fail && strings.Contains(c.Detail, want) {
			return
		}
	}
	t.Errorf("no images failure mentioning %q in %+v", want, imagesChecks(p))
}

// With no `image` directive the hook's images still need somewhere to go:
// the default reference, without core building an image of its own.
func TestImagesHookDefaultsTimeoutAndReference(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "", "image fixer (\n\tsuffix -fixer\n)\n")

	if !p.OK() {
		t.Fatalf("plan failed: %+v", p.Checks)
	}
	if p.Image != nil {
		t.Errorf("core's own image was planned without an `image` directive: %+v", p.Image)
	}
	h := p.ImagesHook
	if h == nil {
		t.Fatal("no images hook was planned")
	}
	if h.Timeout != plan.DefaultImagesTimeout {
		t.Errorf("timeout = %s, want %s", h.Timeout, plan.DefaultImagesTimeout)
	}
	if h.Target.Registry != "ghcr.io" || h.Target.Repository != "you/gambit" {
		t.Errorf("target = %+v, want ghcr.io/you/gambit", h.Target)
	}
}

// Beside core's own image, the hook's images go to the same repository.
func TestImagesHookFollowsTheImageDirective(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "image docker.io/you/talos\n", "image fixer (\n\tsuffix -fixer\n)\n")

	if !p.OK() {
		t.Fatalf("plan failed: %+v", p.Checks)
	}
	if p.Image == nil {
		t.Fatal("core's own image was dropped")
	}
	if got := p.ImagesHook.Target; got.Registry != "docker.io" || got.Repository != "you/talos" {
		t.Errorf("target = %+v, want docker.io/you/talos", got)
	}
}

func TestImagesHookReadsItsTimeout(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "", "timeout 45m\n")
	if !p.OK() {
		t.Fatalf("plan failed: %+v", p.Checks)
	}
	if p.ImagesHook.Timeout != 45*time.Minute {
		t.Errorf("timeout = %s, want 45m", p.ImagesHook.Timeout)
	}
}

func TestImagesHookRejectsABadTimeout(t *testing.T) {
	fakeDocker(t, true)
	for _, tc := range []struct{ name, mod, want string }{
		{"over the cap", "timeout 3h\n", "more than"},
		{"not a duration", "timeout soon\n", "not a duration"},
		{"zero", "timeout 0s\n", "not a duration"},
		{"repeated", "timeout 10m\ntimeout 20m\n", "already set"},
		{"no value", "timeout\n", "expected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := imagesRepo(t, "", tc.mod)
			failsWith(t, p, tc.want)
			if p.ImagesHook != nil {
				t.Error("a hook with a bad timeout was planned anyway")
			}
		})
	}
}

// letsgo-docker needs no config of its own: it builds ./Dockerfile.
func TestImagesHookNeedsNoConfig(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "", "")
	if !p.OK() {
		t.Fatalf("plan failed: %+v", p.Checks)
	}
	if p.ImagesHook == nil || p.ImagesHook.Timeout != plan.DefaultImagesTimeout {
		t.Errorf("hook = %+v", p.ImagesHook)
	}
}

func TestImagesHookNeedsBuildx(t *testing.T) {
	fakeDocker(t, false)
	failsWith(t, imagesRepo(t, "", "timeout 10m\n"), "docker buildx version")
}

// An image for a platform the release does not build gets no staged binary;
// that is allowed, but worth a word.
func TestImagesHookWarnsAboutAnUnbuiltPlatform(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "", "image fixer (\n\tsuffix -fixer\n\tplatform linux/arm64\n)\n")

	if !p.OK() {
		t.Fatalf("an unbuilt platform should only warn: %+v", p.Checks)
	}
	for _, c := range imagesChecks(p) {
		if c.Status == plan.Warn && strings.Contains(c.Detail, "linux/arm64") {
			if c.Pos == nil || !strings.HasSuffix(c.Pos.File, "docker.mod") {
				t.Errorf("the warning is not placed in docker.mod: %+v", c.Pos)
			}
			return
		}
	}
	t.Errorf("no warning about linux/arm64 in %+v", imagesChecks(p))
}

func TestImagesHookNeedsSomewhereToPush(t *testing.T) {
	fakeDocker(t, true)
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n"+imagesPin)
	r.write(".letsgo/docker.mod", "timeout 10m\n")
	r.commit("v1.0.0")

	failsWith(t, r.resolve(plan.Options{}), "nowhere to push")
}

// A plugin letsgo does not know is not probed for anything: only the
// catalogue says what a plugin needs.
func TestImagesHookProbesOnlyWhatTheCatalogueNames(t *testing.T) {
	fakeDocker(t, false)
	r := newRepo(t)
	r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\nplugin images letsgo-ko v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if !p.OK() {
		t.Fatalf("an unknown images plugin was probed for docker: %+v", p.Checks)
	}
}

func TestImagePluginHandsTheReleaseImageToTheHook(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "image docker.io/you/talos\nimage plugin\n", "")
	if !p.OK() {
		t.Fatalf("plan failed: %+v", p.Checks)
	}
	if p.Image == nil || !p.Image.Plugin {
		t.Fatalf("image = %+v", p.Image)
	}
	if got := p.ImagesHook.Target; got.Repository != "you/talos" {
		t.Errorf("hook target = %+v", got)
	}
}

func TestImagePluginRefuses(t *testing.T) {
	fakeDocker(t, true)
	t.Run("without a pin", func(t *testing.T) {
		r := newRepo(t)
		r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
		r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
		r.write("main.go", "package main\n\nfunc main() {}\n")
		r.write("letsgo.mod", "build linux/amd64\nimage plugin\n")
		r.commit("v1.0.0")

		p := r.resolve(plan.Options{})
		c := check(t, p, "image")
		if c.Status != plan.Fail || !strings.Contains(c.Detail, "no plugin is pinned") {
			t.Errorf("check = %+v", c)
		}
	})
	t.Run("beside a cmd", func(t *testing.T) {
		p := imagesRepo(t, "image plugin\nimage cmd serve\n", "")
		c := check(t, p, "image")
		if c.Status != plan.Fail || !strings.Contains(c.Detail, "would not apply") {
			t.Errorf("check = %+v", c)
		}
	})
}

// A block is one more core image: resolved, reported, and no docker needed.
func TestImageBlocksAreResolvedBesideTheReleaseImage(t *testing.T) {
	r := newRepo(t)
	r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\nimage\nimage cmd serve\n\nimage fixer (\n\tsuffix -fixer\n\tcmd fix\n)\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if !p.OK() {
		t.Fatalf("plan failed: %+v", p.Checks)
	}
	if p.Image.Plugin || len(p.Image.Extra) != 1 {
		t.Fatalf("image = %+v", p.Image)
	}
	extra := p.Image.Extra[0]
	if extra.Suffix != "-fixer" || strings.Join(extra.Cmd, " ") != "fix" || extra.Base != (oci.Reference{}) {
		t.Errorf("extra = %+v", extra)
	}
	var reported bool
	for _, c := range p.Checks {
		if c.Name == "image" && strings.Contains(c.Detail, "fixer") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the block's image was not reported: %+v", p.Checks)
	}
}

// A docker that is only on PATH is not found: PATH is whoever set it last.
func TestImagesHookOverrideMustBeAbsolute(t *testing.T) {
	fakeDocker(t, true)
	t.Setenv("LETSGO_DOCKER", "docker")
	failsWith(t, imagesRepo(t, "", ""), "must be an absolute path")
}

// An image left to the runner's platform, on a release that does not build
// it, gets no staged binary; worth one warning. s390x is a platform no CI
// runner is, so the runner's own is never among the release's targets.
func TestImagesHookWarnsWhenTheRunnersPlatformIsNotBuilt(t *testing.T) {
	fakeDocker(t, true)
	r := newRepo(t)
	r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/s390x\n"+imagesPin)
	r.write(".letsgo/docker.mod", "image fixer (\n\tsuffix -fixer\n)\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	for _, c := range imagesChecks(p) {
		if c.Status == plan.Warn && strings.Contains(c.Detail, "this machine's") {
			return
		}
	}
	t.Errorf("no warning about the runner's platform in %+v", imagesChecks(p))
}

func TestImagesHookRefusesAConfigThatDoesNotParse(t *testing.T) {
	fakeDocker(t, true)
	p := imagesRepo(t, "", "image fixer (\n")
	if p.OK() || p.ImagesHook != nil {
		t.Errorf("a docker.mod that does not parse was accepted: %+v", imagesChecks(p))
	}
}

func TestImageBlockWithABadBaseFails(t *testing.T) {
	r := newRepo(t)
	r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\nimage\n\nimage fixer (\n\tsuffix -fixer\n\tbase bad@sha256:nope\n)\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	c := check(t, p, "image")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "image fixer base") || c.Pos == nil {
		t.Errorf("check = %+v", c)
	}
}

// A suffix that makes a tag too long fails the plan, not the push.
func TestImageBlockWithATooLongSuffixFails(t *testing.T) {
	r := newRepo(t)
	r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
	r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\nimage\n\nimage fixer (\n\tsuffix -"+strings.Repeat("x", 125)+"\n)\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	c := check(t, p, "image")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "128 characters") || c.Pos == nil {
		t.Errorf("check = %+v", c)
	}
}

// A plugin pinned by path is still letsgo-docker: its docker.mod is read and
// its docker probed, rather than an unknown plugin's nothing.
func TestImagesHookPinnedByPathIsStillLetsgoDocker(t *testing.T) {
	for _, works := range []bool{true, false} {
		fakeDocker(t, works)
		r := newRepo(t)
		r.git("remote", "add", "origin", "https://github.com/you/gambit.git")
		r.write("go.mod", "module github.com/you/gambit\n\ngo 1.24\n")
		r.write("main.go", "package main\n\nfunc main() {}\n")
		r.write("letsgo.mod", "build linux/amd64\nplugin images ./bin/letsgo-docker v0.1.0 sha256:"+strings.Repeat("a", 64)+"\n")
		r.write(".letsgo/docker.mod", "timeout 45m\n")
		r.commit("v1.0.0")
		p := r.resolve(plan.Options{})

		if works && (p.ImagesHook == nil || p.ImagesHook.Timeout != 45*time.Minute) {
			t.Errorf("docker.mod was not read for a path pin: %+v", p.ImagesHook)
		}
		if !works && p.ImagesHook != nil {
			t.Error("docker was not probed for a path pin")
		}
	}
}
