package release

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/oci/ocitest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// imagesPlan pins a fake images plugin that runs body, pushing to
// ghcr.io/you/gambit for a prerelease, whose tags are the version and its
// channel and need no git history to work out.
func imagesPlan(t *testing.T, body string) *plan.Plan {
	t.Helper()
	digest := fakePlugin(t, "letsgo-docker", body)
	p := basePlan(t)
	p.Version, p.Tag = "1.2.0-rc.1", "v1.2.0-rc.1"
	p.Git.Commit = "abc123"
	p.Git.CommitTime = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	p.Plugins = map[plugin.Hook]plugin.Plugin{
		plugin.HookImages: {Hook: plugin.HookImages, Command: "letsgo-docker", Digest: digest},
	}
	p.ImagesHook = &plan.ImagesHook{
		Target:  &plan.ImageTarget{Registry: "ghcr.io", APIHost: "ghcr.io", Repository: "you/gambit"},
		Timeout: time.Minute,
	}
	return p
}

// linuxArtifact is a built linux/amd64 binary, as the build hands it over.
func linuxArtifact(t *testing.T, content string) build.Artifact {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gambit")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return build.Artifact{
		Archive: "gambit_1.2.0-rc.1_linux_amd64.tar.gz", Target: "linux/amd64", OS: "linux", Arch: "amd64",
		Binaries: []build.Binary{{Name: "gambit"}}, Paths: map[string]string{"gambit": path},
	}
}

// layoutAt writes a layout the fake plugin copies into its output.
func layoutAt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ocitest.WriteLayout(t, dir, ocitest.LayoutOptions{
		Platforms: []oci.Platform{{OS: "linux", Architecture: "amd64"}}, Attest: true,
	})
	return dir
}

// The plugin is handed the staged binaries and a place to write; what it
// writes there is pushed under the release's own tags, suffixed, behind an
// index carrying the release's annotations.
func TestBuildPluginImagesStagesRunsAndSuffixes(t *testing.T) {
	src := layoutAt(t)
	dist := t.TempDir()
	input := filepath.Join(t.TempDir(), "input.json")
	p := imagesPlan(t, `cat > `+input+`
cp -R `+src+` `+filepath.Join(dist, imagesHookDir, "out", "fixer")+`
echo '{"images":[{"suffix":"-fixer","layout":"fixer"}]}'`)

	builds, err := buildPluginImages(context.Background(), p, []build.Artifact{linuxArtifact(t, "ELF")}, dist)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 1 {
		t.Fatalf("got %d images, want 1", len(builds))
	}
	b := builds[0]
	if b.Source != SourcePlugin || b.Layout == nil || b.Repository != "you/gambit" {
		t.Errorf("build = %+v", b)
	}
	if !slices.Equal(b.Tags, []string{"1.2.0-rc.1-fixer", "rc-fixer"}) {
		t.Errorf("tags = %v, want the release's own, suffixed", b.Tags)
	}
	if got := b.Platforms(); !slices.Equal(got, []string{"linux/amd64"}) {
		t.Errorf("platforms = %v", got)
	}

	var index oci.Index
	if err := json.Unmarshal(b.Index.Content, &index); err != nil {
		t.Fatal(err)
	}
	if index.Annotations["org.opencontainers.image.version"] != "1.2.0-rc.1" {
		t.Errorf("index annotations = %v; a floating tag reads the version from here", index.Annotations)
	}

	var in plugin.ImagesInput
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Binaries) != 1 || in.Binaries[0].Path != "linux_amd64/gambit" {
		t.Fatalf("binaries = %+v", in.Binaries)
	}
	staged, err := os.ReadFile(filepath.Join(in.Context, "linux_amd64", "gambit"))
	if err != nil || string(staged) != "ELF" {
		t.Errorf("staged binary = %q, %v; want the release's own bytes", staged, err)
	}
	if in.Date != "2026-10-13T12:00:00Z" || in.Commit != "abc123" || in.ConfigDir == "" {
		t.Errorf("input = %+v", in)
	}

	records := imageRecords(builds)
	if records[0].Source != SourcePlugin || records[0].Digest != string(b.Index.Digest) {
		t.Errorf("record = %+v", records[0])
	}
}

func TestBuildPluginImagesRefusesABadAnswer(t *testing.T) {
	outside := layoutAt(t)
	for _, tc := range []struct{ name, answer, want string }{
		{"two release images", `{"images":[{"suffix":"","layout":"x"},{"suffix":"","layout":"x"}]}`, "only one"},
		{"a suffix with a slash", `{"images":[{"suffix":"/fixer","layout":"x"}]}`, "suffix"},
		{"a repeated suffix", `{"images":[{"suffix":"-a","layout":"x"},{"suffix":"-a","layout":"x"}]}`, "already used"},
		{"a layout outside the output", `{"images":[{"suffix":"-a","layout":"` + outside + `"}]}`, "not inside"},
		{"a layout escaping by ..", `{"images":[{"suffix":"-a","layout":"../context"}]}`, "not inside"},
		{"not a layout", `{"images":[{"suffix":"-a","layout":"x"}]}`, "not an OCI image layout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			p := imagesPlan(t, `cat > /dev/null
mkdir -p `+filepath.Join(dist, imagesHookDir, "out", "x")+`
echo '`+tc.answer+`'`)

			_, err := buildPluginImages(context.Background(), p, nil, dist)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestBuildPluginImagesNoOpWithoutTheHook(t *testing.T) {
	p := basePlan(t)
	builds, err := buildPluginImages(context.Background(), p, nil, t.TempDir())
	if err != nil || builds != nil {
		t.Errorf("buildPluginImages = %v, %v, want nil, nil", builds, err)
	}
}

// A plugin that fails stops the release, with what it said.
func TestBuildPluginImagesStopsOnAFailedBuild(t *testing.T) {
	p := imagesPlan(t, `cat > /dev/null; echo 'ERROR: failed to solve: fixer: not found' >&2; exit 1`)

	_, err := buildPluginImages(context.Background(), p, nil, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "failed to solve") {
		t.Errorf("err = %v", err)
	}
}

// A variant builds the same binary for the same platform; only the
// release's own is staged, so the Dockerfile sees one.
func TestStageBinariesSkipsVariants(t *testing.T) {
	p := basePlan(t)
	p.Version = "1.2.0"
	p.Groups = []plan.Group{{Name: "gambit-gui", Variant: "gui"}}

	own := linuxArtifact(t, "own")
	own.Archive = "gambit_1.2.0_linux_amd64.tar.gz"
	gui := linuxArtifact(t, "gui")
	gui.Archive = "gambit-gui_1.2.0_linux_amd64.tar.gz"

	contextDir := filepath.Join(t.TempDir(), "context")
	staged, err := stageBinaries(p, []build.Artifact{own, gui}, contextDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 1 {
		t.Fatalf("staged = %+v, want only the release's own", staged)
	}
	data, err := os.ReadFile(filepath.Join(contextDir, "linux_amd64", "gambit"))
	if err != nil || string(data) != "own" {
		t.Errorf("staged %q, %v", data, err)
	}
}

// Whether the hook may build the release image is letsgo.mod's call: `image
// plugin` requires it, no image directive allows it, and core building its
// own forbids it.
func TestBuildPluginImagesHoldsTheReleaseImageToLetsgoMod(t *testing.T) {
	answer := func(suffixes ...string) string {
		var parts []string
		for _, s := range suffixes {
			parts = append(parts, `{"suffix":"`+s+`","layout":"x"}`)
		}
		return `{"images":[` + strings.Join(parts, ",") + `]}`
	}
	coreImage := &plan.ImageTarget{Registry: "ghcr.io", APIHost: "ghcr.io", Repository: "you/gambit"}
	pluginImage := &plan.ImageTarget{Registry: "ghcr.io", APIHost: "ghcr.io", Repository: "you/gambit", Plugin: true}
	withBlock := &plan.ImageTarget{
		Registry: "ghcr.io", APIHost: "ghcr.io", Repository: "you/gambit",
		Extra: []plan.ExtraImage{{Name: "fixer", Suffix: "-fixer"}},
	}

	for _, tc := range []struct {
		name   string
		image  *plan.ImageTarget
		answer string
		want   string // "" means the rules pass, and loading the empty layout is what fails
	}{
		{"image plugin with a release image", pluginImage, answer(""), ""},
		{"image plugin without one", pluginImage, answer("-a"), "leave the suffix off"},
		{"no image directive, a release image", nil, answer(""), ""},
		{"no image directive, none", nil, answer("-a"), ""},
		{"core's own release image", coreImage, answer(""), "add `image plugin`"},
		{"core's own, suffixed", coreImage, answer("-a"), ""},
		{"a suffix an image block has", withBlock, answer("-fixer"), "already used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			p := imagesPlan(t, `cat > /dev/null
mkdir -p `+filepath.Join(dist, imagesHookDir, "out", "x")+`
echo '`+tc.answer+`'`)
			p.Image = tc.image

			_, err := buildPluginImages(context.Background(), p, nil, dist)
			want := tc.want
			if want == "" {
				want = "not an OCI image layout"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one mentioning %q", err, want)
			}
		})
	}
}

// An image block is core's: the same binary, its own cmd, its tags suffixed,
// and nothing taken from the release image's settings.
func TestBuildImagesBuildsEachBlock(t *testing.T) {
	p := basePlan(t)
	p.Version = "1.2.0-rc.1"
	p.Git.CommitTime = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	p.Image = &plan.ImageTarget{
		Registry: "ghcr.io", APIHost: "ghcr.io", Repository: "you/gambit",
		Cmd:       []string{"serve"},
		Platforms: []gobuild.Target{{OS: "linux", Arch: "amd64"}},
		Extra:     []plan.ExtraImage{{Name: "fixer", Suffix: "-fixer", Cmd: []string{"fix"}}},
	}

	builds, err := buildImages(context.Background(), p, []build.Artifact{linuxArtifact(t, "ELF")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 {
		t.Fatalf("got %d images, want the release image and the block", len(builds))
	}
	release, fixer := builds[0], builds[1]
	if !slices.Equal(release.Tags, []string{"1.2.0-rc.1", "rc"}) || !slices.Equal(fixer.Tags, []string{"1.2.0-rc.1-fixer", "rc-fixer"}) {
		t.Errorf("tags = %v and %v", release.Tags, fixer.Tags)
	}
	if release.Index.Digest == fixer.Index.Digest {
		t.Error("the block built the same image as the release: its cmd was ignored")
	}
	if fixer.Source != "" {
		t.Errorf("a block is core's own image, but its source is %q", fixer.Source)
	}

	// Under image plugin, core builds the block and nothing else.
	p.Image.Plugin = true
	builds, err = buildImages(context.Background(), p, []build.Artifact{linuxArtifact(t, "ELF")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 1 || builds[0].Tags[0] != "1.2.0-rc.1-fixer" {
		t.Errorf("under image plugin, built %+v", builds)
	}
}
