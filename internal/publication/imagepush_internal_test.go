package publication

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/release"

	"github.com/danielriddell21/letsgo/internal/oci"
)

// fakeManifests answers GET /v2/<repo>/manifests/<tag> from a fixed map of
// tag -> version, 404ing anything not in it, and accepting any other request
// (a floating-tag push writes with PUT, which resolveFloatingTags never
// exercises directly).
type fakeManifests struct {
	current map[string]string
	server  *httptest.Server
}

func newFakeManifests(t *testing.T, current map[string]string) *fakeManifests {
	t.Helper()
	f := &fakeManifests{current: current}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeManifests) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusOK)
		return
	}

	tag := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	version, ok := f.current[tag]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fmt.Fprintf(w, `{"annotations":{"org.opencontainers.image.version":%q}}`, version)
}

func (f *fakeManifests) registry() *oci.Registry {
	r := oci.NewRegistry(strings.TrimPrefix(f.server.URL, "http://"))
	r.Scheme = "http"
	return r
}

// A floating tag that's never been pushed always advances: there's nothing
// yet for the release to be older than.
func TestResolveFloatingTagsAdvancesATagThatHasNeverBeenPushed(t *testing.T) {
	f := newFakeManifests(t, nil)
	built := release.ImageBuild{
		Repository: "you/tool", Version: "1.0.0",
		Tags: []string{"1.0.0"}, Floating: []string{"1", "latest"},
	}

	tags, err := resolveFloatingTags(t.Context(), f.registry(), built, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.0.0", "1", "latest"}
	if !equalTags(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

// A floating tag currently older than the release advances.
func TestResolveFloatingTagsAdvancesATagOlderThanTheRelease(t *testing.T) {
	f := newFakeManifests(t, map[string]string{"1": "1.1.0"})
	built := release.ImageBuild{
		Repository: "you/tool", Version: "1.2.0",
		Tags: []string{"1.2.0"}, Floating: []string{"1"},
	}

	tags, err := resolveFloatingTags(t.Context(), f.registry(), built, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.2.0", "1"}
	if !equalTags(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

// A floating tag already newer than or equal to the release is left alone.
func TestResolveFloatingTagsKeepsATagNewerThanTheRelease(t *testing.T) {
	f := newFakeManifests(t, map[string]string{"1": "1.3.0", "latest": "1.2.0"})
	built := release.ImageBuild{
		Repository: "you/tool", Version: "1.2.0",
		Tags: []string{"1.2.0"}, Floating: []string{"1", "latest"},
	}

	tags, err := resolveFloatingTags(t.Context(), f.registry(), built, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.2.0"}
	if !equalTags(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

// Acceptance scenario 2: v1.3.0 already exists, v1.2.9 releases as a
// backport. It must move 1.2 (which is behind it) but not 1 or latest
// (which already point at 1.3.0).
func TestResolveFloatingTagsBackportMovesOnlyItsOwnMinor(t *testing.T) {
	f := newFakeManifests(t, map[string]string{
		"1.2": "1.2.8", "1": "1.3.0", "latest": "1.3.0",
	})
	built := release.ImageBuild{
		Repository: "you/tool", Version: "1.2.9",
		Tags: []string{"1.2.9"}, Floating: []string{"1.2", "1", "latest"},
	}

	tags, err := resolveFloatingTags(t.Context(), f.registry(), built, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.2.9", "1.2"}
	if !equalTags(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func equalTags(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, tag := range want {
		if got[i] != tag {
			return false
		}
	}
	return true
}
