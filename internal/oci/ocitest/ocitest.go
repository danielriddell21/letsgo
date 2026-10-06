// Package ocitest is an in-process container registry for tests: enough of the
// distribution API to exercise a real push and the reads that decide whether
// to make one.
package ocitest

import (
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

// URL is where the registry is served.
func (f *Registry) URL() string { return f.server.URL }

// Registry implements enough of the distribution API to exercise a real push:
// the token dance, blob existence checks, upload sessions, cross-repo mounts
// and manifest writes.
type Registry struct {
	// Mu is held while the maps below are read or written.
	Mu sync.Mutex

	// Blobs and Manifests are keyed by "<repo>/<digest>" and "<repo>/<ref>".
	Blobs     map[string][]byte
	Manifests map[string][]byte

	// Uploads maps a session id to the repository it belongs to.
	Uploads map[string]string
	next    int

	// RequireToken makes the registry answer 401 until a bearer token is
	// presented, which is what every real registry does.
	RequireToken bool
	// TokenIssued counts the bearer tokens handed out.
	TokenIssued int

	// RefuseMounts makes cross-repo mounts fall back to a real upload.
	RefuseMounts bool

	server *httptest.Server
}

// New starts a registry that requires a bearer token, as every real one does.
func New(tb testing.TB) *Registry {
	tb.Helper()
	f := &Registry{
		Blobs:        map[string][]byte{},
		Manifests:    map[string][]byte{},
		Uploads:      map[string]string{},
		RequireToken: true,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	tb.Cleanup(f.server.Close)
	return f
}

// Client returns an oci.Registry pointed at the fake.
func (f *Registry) Client() *oci.Registry {
	host := strings.TrimPrefix(f.server.URL, "http://")
	r := oci.NewRegistry(host)
	r.Scheme = "http"
	r.Username, r.Password = "x", "token"
	return r
}

func (f *Registry) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		f.Mu.Lock()
		f.TokenIssued++
		f.Mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"token": "issued"})
		return
	}

	if f.RequireToken && r.Header.Get("Authorization") != "Bearer issued" {
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

func (f *Registry) upload(w http.ResponseWriter, r *http.Request) {
	repo, _ := repoAndRest(r.URL.Path, "blobs")

	f.Mu.Lock()
	defer f.Mu.Unlock()

	switch r.Method {
	case http.MethodPost:
		if from := r.URL.Query().Get("from"); from != "" && !f.RefuseMounts {
			digest := r.URL.Query().Get("mount")
			if content, ok := f.Blobs[from+"/"+digest]; ok {
				f.Blobs[repo+"/"+digest] = content
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		f.next++
		id := fmt.Sprintf("session-%d", f.next)
		f.Uploads[id] = repo
		// Deliberately relative: the specification permits it, and a client
		// that assumes absolute breaks against half the registries in use.
		w.Header().Set("Location", "/v2/upload/"+id)
		w.WriteHeader(http.StatusAccepted)

	case http.MethodPut:
		id := strings.TrimPrefix(r.URL.Path, "/v2/upload/")
		target, ok := f.Uploads[id]
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
		f.Blobs[target+"/"+digest] = content
		delete(f.Uploads, id)
		w.WriteHeader(http.StatusCreated)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *Registry) blob(w http.ResponseWriter, r *http.Request) {
	repo, digest := repoAndRest(r.URL.Path, "blobs")

	f.Mu.Lock()
	content, ok := f.Blobs[repo+"/"+digest]
	f.Mu.Unlock()

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

func (f *Registry) manifest(w http.ResponseWriter, r *http.Request) {
	repo, ref := repoAndRest(r.URL.Path, "manifests")

	f.Mu.Lock()
	defer f.Mu.Unlock()

	if r.Method == http.MethodPut {
		content, _ := io.ReadAll(r.Body)
		f.Manifests[repo+"/"+ref] = content
		f.Manifests[repo+"/"+string(oci.DigestOf(content))] = content
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusCreated)
		return
	}

	content, ok := f.Manifests[repo+"/"+ref]
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

// Has reports whether the repository holds a blob.
func (f *Registry) Has(repo string, d oci.Digest) bool {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	_, ok := f.Blobs[repo+"/"+string(d)]
	return ok
}
