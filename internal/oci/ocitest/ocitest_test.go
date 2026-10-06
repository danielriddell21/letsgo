package ocitest_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/oci/ocitest"
)

// Every registry test trusts this fake to behave like a real one, so the fake
// is held to the parts of the distribution API it claims to implement.

func do(t *testing.T, r *ocitest.Registry, method, path string, body string, authed bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, r.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if authed {
		req.Header.Set("Authorization", "Bearer issued")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	return resp, string(got)
}

func push(t *testing.T, r *ocitest.Registry, repo, content string) oci.Digest {
	t.Helper()
	digest := oci.DigestOf([]byte(content))
	resp, _ := do(t, r, http.MethodPost, "/v2/"+repo+"/blobs/uploads/", "", true)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start upload = %d", resp.StatusCode)
	}
	resp, _ = do(t, r, http.MethodPut, resp.Header.Get("Location")+"?digest="+string(digest), content, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("finish upload = %d", resp.StatusCode)
	}
	return digest
}

func TestARegistryDemandsATokenAndIssuesOne(t *testing.T) {
	r := ocitest.New(t)

	resp, _ := do(t, r, http.MethodGet, "/v2/x/blobs/sha256:none", "", false)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "/token") {
		t.Errorf("unauthenticated = %d %q, want 401 naming the token realm", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if _, body := do(t, r, http.MethodGet, "/token", "", false); !strings.Contains(body, "issued") {
		t.Errorf("token = %q", body)
	}
	if r.TokenIssued != 1 {
		t.Errorf("TokenIssued = %d, want 1", r.TokenIssued)
	}

	r.RequireToken = false
	if resp, _ := do(t, r, http.MethodGet, "/v2/x/blobs/sha256:none", "", false); resp.StatusCode != http.StatusNotFound {
		t.Errorf("no token required = %d, want the blob's 404", resp.StatusCode)
	}
}

func TestABlobIsUploadedAndRead(t *testing.T) {
	r := ocitest.New(t)
	digest := push(t, r, "you/app", "layer bytes")

	if !r.Has("you/app", digest) || r.Has("you/other", digest) {
		t.Errorf("Has: stored under you/app only")
	}
	if resp, _ := do(t, r, http.MethodHead, "/v2/you/app/blobs/"+string(digest), "", true); resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD = %d", resp.StatusCode)
	}
	if _, body := do(t, r, http.MethodGet, "/v2/you/app/blobs/"+string(digest), "", true); body != "layer bytes" {
		t.Errorf("GET = %q", body)
	}
	if resp, _ := do(t, r, http.MethodGet, "/v2/you/app/blobs/sha256:missing", "", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing blob = %d, want 404", resp.StatusCode)
	}
}

func TestAnUploadMustMatchItsDigest(t *testing.T) {
	r := ocitest.New(t)

	resp, _ := do(t, r, http.MethodPost, "/v2/you/app/blobs/uploads/", "", true)
	if resp, _ := do(t, r, http.MethodPut, resp.Header.Get("Location")+"?digest=sha256:wrong", "bytes", true); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("wrong digest = %d, want 400", resp.StatusCode)
	}
	if resp, _ := do(t, r, http.MethodPut, "/v2/upload/no-such-session?digest=x", "bytes", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session = %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, r, http.MethodDelete, "/v2/you/app/blobs/uploads/", "", true); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE = %d, want 405", resp.StatusCode)
	}
}

func TestABlobIsMountedFromAnotherRepositoryUnlessRefused(t *testing.T) {
	r := ocitest.New(t)
	digest := push(t, r, "you/app", "shared layer")

	mount := "/v2/you/copy/blobs/uploads/?from=you/app&mount=" + string(digest)
	if resp, _ := do(t, r, http.MethodPost, mount, "", true); resp.StatusCode != http.StatusCreated || !r.Has("you/copy", digest) {
		t.Errorf("mount = %d, stored = %v, want 201 and stored", resp.StatusCode, r.Has("you/copy", digest))
	}

	r.RefuseMounts = true
	if resp, _ := do(t, r, http.MethodPost, "/v2/you/third/blobs/uploads/?from=you/app&mount="+string(digest), "", true); resp.StatusCode != http.StatusAccepted {
		t.Errorf("refused mount = %d, want a fresh upload session (202)", resp.StatusCode)
	}
}

func TestAManifestIsWrittenByTagAndDigest(t *testing.T) {
	r := ocitest.New(t)
	content := `{"mediaType":"application/vnd.oci.image.manifest.v1+json"}`

	resp, _ := do(t, r, http.MethodPut, "/v2/you/app/manifests/latest", content, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT manifest = %d", resp.StatusCode)
	}
	for _, ref := range []string{"latest", string(oci.DigestOf([]byte(content)))} {
		resp, body := do(t, r, http.MethodGet, "/v2/you/app/manifests/"+ref, "", true)
		if body != content || resp.Header.Get("Content-Type") != "application/vnd.oci.image.manifest.v1+json" {
			t.Errorf("GET %s = %q (%s)", ref, body, resp.Header.Get("Content-Type"))
		}
	}
	if resp, _ := do(t, r, http.MethodGet, "/v2/you/app/manifests/none", "", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing manifest = %d, want 404", resp.StatusCode)
	}
}

func TestAnUnknownPathIsNotFoundAndClientPointsAtTheFake(t *testing.T) {
	r := ocitest.New(t)

	if resp, _ := do(t, r, http.MethodGet, "/v2/", "", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/v2/ = %d, want 404", resp.StatusCode)
	}
	if c := r.Client(); c == nil || c.Scheme != "http" {
		t.Errorf("Client = %+v, want a plain-HTTP client", c)
	}
}
