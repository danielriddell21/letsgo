package oci_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

// fakeRegistry implements enough of the distribution API to exercise a real
// push: the token dance, blob existence checks, upload sessions, cross-repo
// mounts and manifest writes.
type fakeRegistry struct {
	mu sync.Mutex

	// blobs and manifests are keyed by "<repo>/<digest>" and "<repo>/<ref>".
	blobs     map[string][]byte
	manifests map[string][]byte

	// uploads maps a session id to the repository it belongs to.
	uploads map[string]string
	next    int

	// requireToken makes the registry answer 401 until a bearer token is
	// presented, which is what every real registry does.
	requireToken bool
	tokenIssued  int

	// refuseMounts makes cross-repo mounts fall back to a real upload.
	refuseMounts bool

	server *httptest.Server
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{
		blobs:        map[string][]byte{},
		manifests:    map[string][]byte{},
		uploads:      map[string]string{},
		requireToken: true,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// client returns a Registry pointed at the fake.
func (f *fakeRegistry) client() *oci.Registry {
	host := strings.TrimPrefix(f.server.URL, "http://")
	r := oci.NewRegistry(host)
	r.Scheme = "http"
	r.Username, r.Password = "x", "token"
	return r
}

func (f *fakeRegistry) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		f.mu.Lock()
		f.tokenIssued++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"token": "issued"})
		return
	}

	if f.requireToken && r.Header.Get("Authorization") != "Bearer issued" {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:x:pull,push"`, f.server.URL))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch {
	case strings.HasPrefix(r.URL.Path, "/v2/upload/"),
		strings.Contains(r.URL.Path, "/blobs/uploads/"):
		f.upload(w, r)
	case strings.Contains(r.URL.Path, "/blobs/"):
		f.blob(w, r)
	case strings.Contains(r.URL.Path, "/manifests/"):
		f.manifest(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// repoAndRest splits "/v2/<repo...>/<kind>/<rest>".
func repoAndRest(path, kind string) (string, string) {
	trimmed := strings.TrimPrefix(path, "/v2/")
	i := strings.Index(trimmed, "/"+kind+"/")
	if i < 0 {
		return "", ""
	}
	return trimmed[:i], trimmed[i+len(kind)+2:]
}

func (f *fakeRegistry) upload(w http.ResponseWriter, r *http.Request) {
	repo, _ := repoAndRest(r.URL.Path, "blobs")

	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.Method {
	case http.MethodPost:
		if from := r.URL.Query().Get("from"); from != "" && !f.refuseMounts {
			digest := r.URL.Query().Get("mount")
			if content, ok := f.blobs[from+"/"+digest]; ok {
				f.blobs[repo+"/"+digest] = content
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		f.next++
		id := fmt.Sprintf("session-%d", f.next)
		f.uploads[id] = repo
		// Deliberately relative: the specification permits it, and a client
		// that assumes absolute breaks against half the registries in use.
		w.Header().Set("Location", "/v2/upload/"+id)
		w.WriteHeader(http.StatusAccepted)

	case http.MethodPut:
		id := strings.TrimPrefix(r.URL.Path, "/v2/upload/")
		target, ok := f.uploads[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		content, _ := io.ReadAll(r.Body)
		digest := r.URL.Query().Get("digest")
		if oci.DigestOf(content) != oci.Digest(digest) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.blobs[target+"/"+digest] = content
		delete(f.uploads, id)
		w.WriteHeader(http.StatusCreated)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeRegistry) blob(w http.ResponseWriter, r *http.Request) {
	repo, digest := repoAndRest(r.URL.Path, "blobs")

	f.mu.Lock()
	content, ok := f.blobs[repo+"/"+digest]
	f.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(content)
}

func (f *fakeRegistry) manifest(w http.ResponseWriter, r *http.Request) {
	repo, ref := repoAndRest(r.URL.Path, "manifests")

	f.mu.Lock()
	defer f.mu.Unlock()

	if r.Method == http.MethodPut {
		content, _ := io.ReadAll(r.Body)
		f.manifests[repo+"/"+ref] = content
		f.manifests[repo+"/"+string(oci.DigestOf(content))] = content
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusCreated)
		return
	}

	content, ok := f.manifests[repo+"/"+ref]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var probe struct {
		MediaType string `json:"mediaType"`
	}
	json.Unmarshal(content, &probe)
	w.Header().Set("Content-Type", probe.MediaType)
	w.Write(content)
}

func (f *fakeRegistry) has(repo string, d oci.Digest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.blobs[repo+"/"+string(d)]
	return ok
}

func TestPushPublishesEveryBlobAndTheIndex(t *testing.T) {
	fake := newFakeRegistry(t)
	reg := fake.client()

	o := options(t)
	amd64, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}
	// A different binary, as a real matrix produces: two platforms sharing one
	// layer would hide whether each image's blobs were pushed.
	o.Binary = binary(t, "different ELF-ish bytes")
	o.Platform = oci.Platform{OS: "linux", Architecture: "arm64"}
	arm64, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	result, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3", "latest"},
		Images: []*oci.Image{amd64, arm64}, Index: indexOf(t, amd64, arm64),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, img := range []*oci.Image{amd64, arm64} {
		if !fake.has("you/tool", img.Layer.Digest) {
			t.Errorf("%s: layer was not uploaded", img.Platform)
		}
		if !fake.has("you/tool", img.ConfigJS.Digest) {
			t.Errorf("%s: config was not uploaded", img.Platform)
		}
	}

	// Every tag must name the same bytes as the index the release assembled and
	// recorded. Rebuilding it here from anything less than identical inputs
	// would publish a digest the manifest does not claim, and `latest` would
	// disagree with the version tag.
	want := indexOf(t, amd64, arm64)
	for _, tag := range []string{"1.2.3", "latest"} {
		fake.mu.Lock()
		published, ok := fake.manifests["you/tool/"+tag]
		fake.mu.Unlock()

		if !ok {
			t.Fatalf("%s was not published", tag)
		}
		if oci.DigestOf(published) != want.Digest {
			t.Errorf("%s is %s, not the assembled index %s",
				tag, oci.DigestOf(published), want.Digest)
		}
	}
	if result.Digest != want.Digest {
		t.Errorf("reported digest %s does not name the published index", result.Digest)
	}
	if strings.Join(result.Platforms, ",") != "linux/amd64,linux/arm64" {
		t.Errorf("platforms = %v", result.Platforms)
	}
	if result.Uploaded != 4 {
		t.Errorf("uploaded %d blobs, want 4", result.Uploaded)
	}
	// The token must be fetched once and reused, not re-fetched per request.
	if fake.tokenIssued != 1 {
		t.Errorf("issued %d tokens, want 1", fake.tokenIssued)
	}
}

// Re-running a release must move no bytes: every blob is content-addressed and
// already there.
func TestPushIsIdempotent(t *testing.T) {
	fake := newFakeRegistry(t)
	reg := fake.client()

	img, err := oci.BuildImage(options(t))
	if err != nil {
		t.Fatal(err)
	}
	push := func() *oci.PushResult {
		t.Helper()
		result, err := oci.Push(context.Background(), oci.PushOptions{
			Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
			Images: []*oci.Image{img}, Index: indexOf(t, img),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	first, second := push(), push()

	if first.Digest != second.Digest {
		t.Errorf("two pushes produced different digests: %s and %s", first.Digest, second.Digest)
	}
	if second.Uploaded != 0 || second.Skipped != 2 {
		t.Errorf("the second push uploaded %d and skipped %d, want 0 and 2",
			second.Uploaded, second.Skipped)
	}
}

func TestPushMountsBaseLayersRatherThanCopyingThem(t *testing.T) {
	fake := newFakeRegistry(t)
	reg := fake.client()

	baseLayer := []byte("base layer bytes")
	baseDigest := oci.DigestOf(baseLayer)
	fake.mu.Lock()
	fake.blobs["distroless/static/"+string(baseDigest)] = baseLayer
	fake.mu.Unlock()

	o := options(t)
	o.Base = &oci.Base{
		Reference: "distroless/static",
		Config:    oci.Config{RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}}},
		Layers: []oci.Descriptor{{
			MediaType: oci.MediaTypeLayerGzip, Digest: baseDigest, Size: int64(len(baseLayer)),
		}},
	}
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	result, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
		Base: &oci.Source{Registry: reg, Repository: "distroless/static"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !fake.has("you/tool", baseDigest) {
		t.Error("the base layer never reached the target repository")
	}
	// A mount transfers nothing, so the base layer must not be counted as an
	// upload.
	if result.Uploaded != 2 {
		t.Errorf("uploaded %d blobs, want 2 (ours only)", result.Uploaded)
	}
}

// A registry may decline a mount for any reason; the layer still has to
// arrive.
func TestPushFallsBackWhenAMountIsRefused(t *testing.T) {
	fake := newFakeRegistry(t)
	fake.refuseMounts = true
	reg := fake.client()

	baseLayer := []byte("base layer bytes")
	baseDigest := oci.DigestOf(baseLayer)
	fake.mu.Lock()
	fake.blobs["distroless/static/"+string(baseDigest)] = baseLayer
	fake.mu.Unlock()

	o := options(t)
	o.Base = &oci.Base{
		Reference: "distroless/static",
		Config:    oci.Config{RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}}},
		Layers: []oci.Descriptor{{
			MediaType: oci.MediaTypeLayerGzip, Digest: baseDigest, Size: int64(len(baseLayer)),
		}},
	}
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := oci.Push(context.Background(), oci.PushOptions{
		Registry: reg, Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
		Base: &oci.Source{Registry: reg, Repository: "distroless/static"},
	}); err != nil {
		t.Fatal(err)
	}

	if !fake.has("you/tool", baseDigest) {
		t.Error("the base layer never reached the target repository")
	}
}

// Stacking on a base whose blobs are unreachable must fail loudly rather than
// publishing a manifest naming layers the registry does not hold.
func TestPushRefusesAnUnreachableBase(t *testing.T) {
	fake := newFakeRegistry(t)

	o := options(t)
	o.Base = &oci.Base{
		Reference: "distroless/static",
		Config:    oci.Config{RootFS: oci.RootFS{Type: "layers", DiffIDs: []oci.Digest{"sha256:base"}}},
		Layers:    []oci.Descriptor{{MediaType: oci.MediaTypeLayerGzip, Digest: oci.Digest("sha256:" + strings.Repeat("a", 64))}},
	}
	img, err := oci.BuildImage(o)
	if err != nil {
		t.Fatal(err)
	}

	_, err = oci.Push(context.Background(), oci.PushOptions{
		Registry: fake.client(), Repository: "you/tool", Tags: []string{"1.2.3"},
		Images: []*oci.Image{img}, Index: indexOf(t, img),
	})
	if err == nil {
		t.Fatal("want an error when a base layer cannot be fetched")
	}
}

// indexOf assembles the index for images, as a release does before pushing.
func indexOf(t *testing.T, images ...*oci.Image) oci.Blob {
	t.Helper()
	index, err := oci.BuildIndex(images, nil)
	if err != nil {
		t.Fatal(err)
	}
	return index
}
