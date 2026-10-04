package forgetest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/github"
)

// Fake is an in-memory forge for one repository: enough of the releases API
// to publish into and read back, and nothing else. A request it does not know
// is answered 404 and remembered, so a test can say which calls were made.
//
// Forge, above, is the reading side (a release published earlier, served);
// Fake is the writing side (a release published during the test).
type Fake struct {
	url, repo string

	mu       sync.Mutex
	nextID   int64
	releases map[string]*github.Release // by tag
	content  map[int64][]byte           // by asset id
	missed   []string
}

// NewFake starts a fake forge serving the releases API of repo ("owner/name").
func NewFake(tb testing.TB, repo string) *Fake {
	tb.Helper()

	ff := &Fake{repo: repo, releases: map[string]*github.Release{}, content: map[int64][]byte{}}
	base := "/repos/" + repo
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base, ff.repoInfo)
	mux.HandleFunc("GET "+base+"/releases/{first}/{second}", ff.getRelease)
	mux.HandleFunc("POST "+base+"/releases", ff.createRelease)
	mux.HandleFunc("PATCH "+base+"/releases/{id}", ff.updateRelease)
	mux.HandleFunc("POST "+base+"/releases/{id}/assets", ff.uploadAsset)
	mux.HandleFunc("DELETE "+base+"/releases/assets/{id}", ff.deleteAsset)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ff.mu.Lock()
		ff.missed = append(ff.missed, r.Method+" "+r.URL.Path)
		ff.mu.Unlock()
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	})

	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)
	ff.url = srv.URL
	return ff
}

// URL is where the fake is served, for a client's endpoints.
func (ff *Fake) URL() string { return ff.url }

// Release returns the release published for tag, or nil.
func (ff *Fake) Release(tag string) *github.Release {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	if r := ff.releases[tag]; r != nil {
		copied := *r
		return &copied
	}
	return nil
}

// Asset returns the bytes the named asset was uploaded with.
func (ff *Fake) Asset(tag, name string) ([]byte, bool) {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	r := ff.releases[tag]
	if r == nil {
		return nil, false
	}
	a, ok := r.Asset(name)
	return ff.content[a.ID], ok
}

// Unhandled lists the requests that matched no route.
func (ff *Fake) Unhandled() []string {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	return append([]string(nil), ff.missed...)
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (ff *Fake) repoInfo(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusOK, map[string]any{
		"permissions": map[string]bool{"push": true},
		"description": "a demo",
		"homepage":    "https://example.test/demo",
		"license":     map[string]string{"spdx_id": "MIT"},
	})
}

// getRelease serves both GET releases/tags/{tag} and GET releases/{id}/assets,
// which a ServeMux cannot tell apart by pattern alone.
func (ff *Fake) getRelease(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("first") != "tags" {
		ff.assets(w, r, r.PathValue("first"))
		return
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	rel := ff.releases[r.PathValue("second")]
	if rel == nil || rel.Draft {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	reply(w, http.StatusOK, rel)
}

func (ff *Fake) createRelease(w http.ResponseWriter, r *http.Request) {
	var in github.ReleaseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	ff.nextID++
	rel := &github.Release{
		ID: ff.nextID, TagName: in.TagName, Name: in.Name, Body: in.Body,
		Draft: in.Draft, Prerelease: in.Prerelease,
		HTMLURL: fmt.Sprintf("%s/%s/releases/tag/%s", ff.url, ff.repo, in.TagName),
	}
	ff.releases[in.TagName] = rel
	reply(w, http.StatusCreated, rel)
}

func (ff *Fake) updateRelease(w http.ResponseWriter, r *http.Request) {
	var in github.ReleaseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	rel := ff.byID(r.PathValue("id"))
	if rel == nil {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	rel.Name, rel.Body, rel.Draft, rel.Prerelease = in.Name, in.Body, in.Draft, in.Prerelease
	reply(w, http.StatusOK, rel)
}

func (ff *Fake) assets(w http.ResponseWriter, _ *http.Request, id string) {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	rel := ff.byID(id)
	if rel == nil {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	reply(w, http.StatusOK, rel.Assets)
}

func (ff *Fake) uploadAsset(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	rel := ff.byID(r.PathValue("id"))
	if rel == nil {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	ff.nextID++
	sum := sha256.Sum256(body)
	asset := github.Asset{
		ID: ff.nextID, Name: r.URL.Query().Get("name"), Size: int64(len(body)),
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}
	rel.Assets = append(rel.Assets, asset)
	ff.content[asset.ID] = body
	reply(w, http.StatusCreated, asset)
}

func (ff *Fake) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ff.mu.Lock()
	defer ff.mu.Unlock()
	for _, rel := range ff.releases {
		for i, a := range rel.Assets {
			if a.ID == id {
				rel.Assets = append(rel.Assets[:i], rel.Assets[i+1:]...)
				delete(ff.content, id)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}
	reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

// byID finds a release by its decimal id; the caller holds ff.mu.
func (ff *Fake) byID(id string) *github.Release {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	for _, rel := range ff.releases {
		if rel.ID == n {
			return rel
		}
	}
	return nil
}
