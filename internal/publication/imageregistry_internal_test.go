package publication

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/oci/ocitest"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/plan"
)

// registryRig is a registry served in process, and the one image build a
// release of "you/tool" assembles: a version tag it always pushes, and a
// floating tag it pushes only when it is newer than what the tag holds.
type registryRig struct {
	fake  *ocitest.Registry
	build release.ImageBuild
}

func newRegistryRig(t *testing.T, version string) *registryRig {
	t.Helper()
	fake := ocitest.New(t)

	bin := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(bin, []byte("ELF-ish "+version), 0o755); err != nil {
		t.Fatal(err)
	}
	annotations := map[string]string{"org.opencontainers.image.version": version}
	img, err := oci.BuildImage(oci.ImageOptions{
		Binary: bin, Name: "tool", Platform: oci.Platform{OS: "linux", Architecture: "amd64"},
		Created: time.Unix(0, 0).UTC(), Annotations: annotations,
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := oci.BuildIndex([]*oci.Image{img}, annotations)
	if err != nil {
		t.Fatal(err)
	}

	return &registryRig{fake: fake, build: release.ImageBuild{
		Registry: "registry.test", APIHost: "registry.test", Repository: "you/tool",
		Version: version, Tags: []string{version}, Floating: []string{"latest"},
		Images: []*oci.Image{img}, Index: index,
	}}
}

func (r *registryRig) registryAt(string) *oci.Registry { return r.fake.Client() }

func (r *registryRig) published(tag string) ([]byte, bool) {
	r.fake.Mu.Lock()
	defer r.fake.Mu.Unlock()
	b, ok := r.fake.Manifests["you/tool/"+tag]
	return b, ok
}

// A release pushes its version tag, and moves a floating tag that has never
// been pushed: both name the index the release assembled and recorded.
func TestPushImagesPublishesTheVersionTagAndAFirstFloatingTag(t *testing.T) {
	r := newRegistryRig(t, "1.2.3")
	var log []string

	err := pushImages(context.Background(), []release.ImageBuild{r.build}, r.registryAt,
		func(format string, args ...any) { log = append(log, format) })
	if err != nil {
		t.Fatal(err)
	}

	for _, tag := range []string{"1.2.3", "latest"} {
		got, ok := r.published(tag)
		if !ok {
			t.Fatalf("%s was not pushed", tag)
		}
		if oci.DigestOf(got) != r.build.Index.Digest {
			t.Errorf("%s is %s, not the assembled index %s", tag, oci.DigestOf(got), r.build.Index.Digest)
		}
	}
	if !r.fake.Has("you/tool", r.build.Images[0].Layer.Digest) {
		t.Error("the layer was not uploaded")
	}
	if len(log) == 0 || !strings.HasPrefix(log[len(log)-1], "pushed ") {
		t.Errorf("log = %v, want a closing 'pushed' line", log)
	}
}

// Floating tags only move forward: a backport must not drag latest back.
func TestPushImagesLeavesAFloatingTagThatIsAlreadyNewer(t *testing.T) {
	newer := newRegistryRig(t, "2.0.0")
	if err := pushImages(context.Background(), []release.ImageBuild{newer.build}, newer.registryAt, nil); err != nil {
		t.Fatal(err)
	}
	latestBefore, _ := newer.published("latest")

	// Releasing 1.9.1 into the same registry, from the same fake.
	older := newRegistryRig(t, "1.9.1")
	older.fake = newer.fake
	if err := pushImages(context.Background(), []release.ImageBuild{older.build}, older.registryAt, nil); err != nil {
		t.Fatal(err)
	}

	if _, ok := older.published("1.9.1"); !ok {
		t.Error("the backport's own version tag was not pushed")
	}
	if latestAfter, _ := older.published("latest"); string(latestAfter) != string(latestBefore) {
		t.Error("a backport moved latest backwards")
	}
}

func TestPushImagesIsIdempotent(t *testing.T) {
	r := newRegistryRig(t, "1.2.3")
	for i := range 2 {
		if err := pushImages(context.Background(), []release.ImageBuild{r.build}, r.registryAt, nil); err != nil {
			t.Fatalf("push %d: %v", i+1, err)
		}
	}
	if r.fake.TokenIssued < 1 {
		t.Error("the registry never asked for a token")
	}
}

func TestPushImagesFailsForAnUnparseableVersionBeforeWritingAnything(t *testing.T) {
	r := newRegistryRig(t, "1.2.3")
	r.build.Version = "not-a-version"

	err := pushImages(context.Background(), []release.ImageBuild{r.build}, r.registryAt, nil)
	if err == nil || !strings.Contains(err.Error(), "floating tags can be compared") {
		t.Fatalf("err = %v, want a refusal to compare floating tags", err)
	}
	if _, ok := r.published("1.2.3"); ok {
		t.Error("a tag was pushed despite the error")
	}
}

func TestObserveImagesSaysWhatPushingWouldChange(t *testing.T) {
	r := newRegistryRig(t, "1.2.3")
	ctx := context.Background()

	before, err := observeImages(ctx, []release.ImageBuild{r.build}, r.registryAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 || before[0].Op != plan.Add || before[1].Op != plan.Add {
		t.Fatalf("against an empty registry: %+v, want two additions", before)
	}
	if before[0].Target != "registry.test/you/tool:1.2.3" || before[0].Planned != string(r.build.Index.Digest) {
		t.Errorf("action = %+v", before[0])
	}

	if err := pushImages(ctx, []release.ImageBuild{r.build}, r.registryAt, nil); err != nil {
		t.Fatal(err)
	}
	after, err := observeImages(ctx, []release.ImageBuild{r.build}, r.registryAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].Op != plan.Keep || after[1].Op != plan.Keep {
		t.Errorf("after pushing: %+v, want nothing to change", after)
	}
}
