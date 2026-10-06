package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

var testRepo = Repo{Owner: "you", Name: "foo"}

// request is what a handler saw, so a test can assert on it after the call.
type request struct {
	method, path, rawQuery string
	header                 http.Header
	body                   []byte
	contentLength          int64
}

// record serves status and body for every request and keeps the last one.
func record(t *testing.T, status int, body any) (*Client, *request) {
	t.Helper()
	got := &request{}
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		*got = request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), data, r.ContentLength}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	})
	return c, got
}

func TestEveryRequestCarriesTheHeaders(t *testing.T) {
	c, got := record(t, http.StatusOK, map[string]any{"id": 1})
	c.UserAgent = "letsgo-test"
	if _, err := c.ReleaseByTag(context.Background(), testRepo, "v1"); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"Authorization":        "Bearer token",
		"Accept":               "application/vnd.github+json",
		"User-Agent":           "letsgo-test",
		"X-Github-Api-Version": apiVersion,
	} {
		if got.header.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.header.Get(k), want)
		}
	}
}

func TestReleaseByTag(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		c, got := record(t, http.StatusOK, Release{ID: 7, TagName: "v1.0.0", Draft: true, Assets: []Asset{{ID: 1, Name: "a.tgz"}}})
		rel, err := c.ReleaseByTag(context.Background(), testRepo, "v1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if got.method != "GET" || got.path != "/repos/you/foo/releases/tags/v1.0.0" {
			t.Errorf("request = %s %s", got.method, got.path)
		}
		if rel.ID != 7 || !rel.Draft {
			t.Errorf("release = %+v", rel)
		}
		if _, ok := rel.Asset("a.tgz"); !ok {
			t.Error("Asset(a.tgz) not found")
		}
	})
	t.Run("404 is nil, nil", func(t *testing.T) {
		c, _ := record(t, http.StatusNotFound, map[string]string{"message": "Not Found"})
		rel, err := c.ReleaseByTag(context.Background(), testRepo, "v9")
		if rel != nil || err != nil {
			t.Errorf("got %v, %v, want nil, nil", rel, err)
		}
	})
	t.Run("other errors surface", func(t *testing.T) {
		c, _ := record(t, http.StatusInternalServerError, map[string]string{"message": "boom"})
		_, err := c.ReleaseByTag(context.Background(), testRepo, "v1")
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Message != "boom" {
			t.Errorf("err = %v, want a 500 APIError", err)
		}
	})
}

func TestLatestRelease(t *testing.T) {
	c, got := record(t, http.StatusOK, Release{ID: 3, TagName: "v2"})
	rel, err := c.LatestRelease(context.Background(), testRepo)
	if err != nil || rel.TagName != "v2" || got.path != "/repos/you/foo/releases/latest" {
		t.Fatalf("rel = %+v, err = %v, path = %s", rel, err, got.path)
	}

	c, _ = record(t, http.StatusNotFound, nil)
	if rel, err := c.LatestRelease(context.Background(), testRepo); rel != nil || err != nil {
		t.Errorf("404: got %v, %v, want nil, nil", rel, err)
	}
}

// pages serves n items per page for the given number of full pages, then a
// short final page, and records the page numbers asked for.
func pages(t *testing.T, full int, item func(i int) any) (*Client, *[]string) {
	t.Helper()
	var asked []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		asked = append(asked, r.URL.Query().Get("page"))
		n := 100
		if page > full {
			n = 3
		}
		batch := make([]any, n)
		for i := range batch {
			batch[i] = item((page-1)*100 + i)
		}
		_ = json.NewEncoder(w).Encode(batch)
	})
	return c, &asked
}

func TestListReleasesPaginatesToTheShortPage(t *testing.T) {
	c, asked := pages(t, 2, func(i int) any { return Release{ID: int64(i)} })
	got, err := c.ListReleases(context.Background(), testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 203 {
		t.Errorf("len = %d, want 203", len(got))
	}
	if strings.Join(*asked, ",") != "1,2,3" {
		t.Errorf("pages asked = %v, want 1,2,3", *asked)
	}
}

func TestAssetsPaginatesToTheShortPage(t *testing.T) {
	c, asked := pages(t, 1, func(i int) any { return Asset{ID: int64(i), Name: fmt.Sprint("a", i)} })
	got, err := c.Assets(context.Background(), testRepo, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 103 || strings.Join(*asked, ",") != "1,2" {
		t.Errorf("len = %d, pages = %v, want 103 over pages 1,2", len(got), *asked)
	}
}

func TestListingErrorsSurface(t *testing.T) {
	c, _ := record(t, http.StatusForbidden, map[string]string{"message": "no"})
	if _, err := c.ListReleases(context.Background(), testRepo); err == nil {
		t.Error("ListReleases: want an error")
	}
	if _, err := c.Assets(context.Background(), testRepo, 1); err == nil {
		t.Error("Assets: want an error")
	}
}

func TestCreateAndUpdateRelease(t *testing.T) {
	in := ReleaseInput{TagName: "v1.0.0", Name: "one", Draft: true, MakeLatest: "true", TargetCommitish: "abc"}

	t.Run("create", func(t *testing.T) {
		c, got := record(t, http.StatusCreated, Release{ID: 5, HTMLURL: "https://x/5"})
		rel, err := c.CreateRelease(context.Background(), testRepo, in)
		if err != nil {
			t.Fatal(err)
		}
		if got.method != "POST" || got.path != "/repos/you/foo/releases" || got.header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s (%s)", got.method, got.path, got.header.Get("Content-Type"))
		}
		var sent ReleaseInput
		if err := json.Unmarshal(got.body, &sent); err != nil || sent != in {
			t.Errorf("sent = %+v (%v), want %+v", sent, err, in)
		}
		if rel.ID != 5 || rel.HTMLURL != "https://x/5" {
			t.Errorf("release = %+v", rel)
		}
	})
	t.Run("update", func(t *testing.T) {
		c, got := record(t, http.StatusOK, Release{ID: 5})
		if _, err := c.UpdateRelease(context.Background(), testRepo, 5, in); err != nil {
			t.Fatal(err)
		}
		if got.method != "PATCH" || got.path != "/repos/you/foo/releases/5" {
			t.Errorf("request = %s %s", got.method, got.path)
		}
	})
	t.Run("validation errors are reported with their details", func(t *testing.T) {
		c, _ := record(t, http.StatusUnprocessableEntity, map[string]any{
			"message": "Validation Failed",
			"errors": []map[string]string{
				{"resource": "Release", "field": "tag_name", "code": "already_exists"},
				{"message": "free text"},
			},
		})
		_, err := c.CreateRelease(context.Background(), testRepo, in)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || len(apiErr.Errors) != 2 ||
			apiErr.Errors[0] != "Release.tag_name: already_exists" || apiErr.Errors[1] != "free text" {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(err.Error(), "Validation Failed") {
			t.Errorf("error text = %q", err)
		}
	})
}

func TestDeleteAsset(t *testing.T) {
	c, got := record(t, http.StatusNoContent, nil)
	if err := c.DeleteAsset(context.Background(), testRepo, 42); err != nil {
		t.Fatal(err)
	}
	if got.method != "DELETE" || got.path != "/repos/you/foo/releases/assets/42" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
}

func TestUploadAssetSendsABoundedOctetStreamToTheUploadHost(t *testing.T) {
	var apiHits int
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { apiHits++ }))
	t.Cleanup(api.Close)

	var got request
	upload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got = request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), data, r.ContentLength}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Asset{ID: 9, Name: "my file+1.tar.gz", Size: 5})
	}))
	t.Cleanup(upload.Close)

	c := New("token")
	c.SetEndpoints(api.URL+"/", upload.URL+"/")

	asset, err := c.UploadAsset(context.Background(), testRepo, 7, "my file+1.tar.gz", 5, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if apiHits != 0 {
		t.Errorf("the API host was hit %d times, want the upload host only", apiHits)
	}
	if got.method != "POST" || got.path != "/repos/you/foo/releases/7/assets" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	if got.rawQuery != "name=my%20file%2B1.tar.gz" {
		t.Errorf("query = %q, want the name percent-escaped", got.rawQuery)
	}
	if got.header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got.header.Get("Content-Type"))
	}
	if got.contentLength != 5 || string(got.body) != "hello" {
		t.Errorf("content length = %d, body = %q; an upload must not be chunked", got.contentLength, got.body)
	}
	if asset.ID != 9 {
		t.Errorf("asset = %+v", asset)
	}
}

func TestCheckAccess(t *testing.T) {
	t.Run("reports what the token may do", func(t *testing.T) {
		c, got := record(t, http.StatusOK, map[string]any{"permissions": map[string]bool{"push": true}, "archived": true})
		access, err := c.CheckAccess(context.Background(), testRepo)
		if err != nil {
			t.Fatal(err)
		}
		if got.path != "/repos/you/foo" || !access.CanPush || !access.Archived {
			t.Errorf("path = %s, access = %+v", got.path, access)
		}
	})
	t.Run("a 404 explains itself", func(t *testing.T) {
		c, _ := record(t, http.StatusNotFound, nil)
		_, err := c.CheckAccess(context.Background(), testRepo)
		if err == nil || !strings.Contains(err.Error(), "not visible to this token") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("other errors pass through", func(t *testing.T) {
		c, _ := record(t, http.StatusBadGateway, nil)
		if _, err := c.CheckAccess(context.Background(), testRepo); err == nil || strings.Contains(err.Error(), "not visible") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestTags(t *testing.T) {
	c, got := record(t, http.StatusOK, []Tag{{Name: "v1"}, {Name: "v2"}})
	names, err := c.Tags(context.Background(), testRepo, 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "v1,v2" || got.rawQuery != "per_page=5" {
		t.Errorf("names = %v, query = %q", names, got.rawQuery)
	}

	for _, limit := range []int{0, -1, 1000} {
		if _, err := c.Tags(context.Background(), testRepo, limit); err != nil {
			t.Fatal(err)
		}
		if got.rawQuery != "per_page=100" {
			t.Errorf("limit %d: query = %q, want it clamped to 100", limit, got.rawQuery)
		}
	}
}

func TestCompare(t *testing.T) {
	c, got := record(t, http.StatusOK, map[string]any{"commits": []map[string]any{
		{"sha": "a1", "commit": map[string]any{"message": "feat: x", "author": map[string]string{"name": "Dan"}}, "author": map[string]string{"login": "dan"}},
		{"sha": "b2"},
	}})
	commits, err := c.Compare(context.Background(), testRepo, "v1", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/repos/you/foo/compare/v1...v2" {
		t.Errorf("path = %s", got.path)
	}
	if len(commits) != 2 || commits[0].SHA != "a1" || commits[0].Commit.Message != "feat: x" ||
		commits[0].Commit.Author.Name != "Dan" || commits[0].Author == nil || commits[0].Author.Login != "dan" {
		t.Errorf("commits = %+v", commits)
	}
	if commits[1].Author != nil {
		t.Errorf("a commit with no linked user should have a nil Author, got %+v", commits[1].Author)
	}
}

func TestDownloadAsset(t *testing.T) {
	t.Run("asks for the raw bytes", func(t *testing.T) {
		var got request
		c := serve(t, func(w http.ResponseWriter, r *http.Request) {
			got = request{method: r.Method, path: r.URL.Path, header: r.Header.Clone()}
			_, _ = w.Write([]byte("binary\x00bytes"))
		})
		data, err := c.DownloadAsset(context.Background(), testRepo, 11)
		if err != nil {
			t.Fatal(err)
		}
		if got.path != "/repos/you/foo/releases/assets/11" || got.header.Get("Accept") != "application/octet-stream" {
			t.Errorf("path = %s, Accept = %q", got.path, got.header.Get("Accept"))
		}
		if string(data) != "binary\x00bytes" {
			t.Errorf("data = %q", data)
		}
	})
	t.Run("a failure is an APIError", func(t *testing.T) {
		c, _ := record(t, http.StatusNotFound, map[string]string{"message": "gone"})
		_, err := c.DownloadAsset(context.Background(), testRepo, 11)
		if !NotFound(err) {
			t.Errorf("err = %v, want a 404 APIError", err)
		}
	})
}

func TestAttestations(t *testing.T) {
	sum := strings.Repeat("ab", 32)

	c, got := record(t, http.StatusOK, map[string]any{"attestations": []map[string]any{
		{"repository_id": 99, "bundle": map[string]any{"mediaType": "application/vnd.dev.sigstore.bundle+json"}},
	}})
	atts, err := c.Attestations(context.Background(), testRepo, sum)
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/repos/you/foo/attestations/sha256:"+sum {
		t.Errorf("path = %s", got.path)
	}
	if len(atts) != 1 || atts[0].RepositoryID != 99 || !strings.Contains(atts[0].Bundle.MediaType, "sigstore") {
		t.Errorf("attestations = %+v", atts)
	}

	// Most releases have none, which is an answer and not a failure.
	c, _ = record(t, http.StatusNotFound, nil)
	if atts, err := c.Attestations(context.Background(), testRepo, sum); atts != nil || err != nil {
		t.Errorf("404: got %v, %v, want nil, nil", atts, err)
	}
	c, _ = record(t, http.StatusInternalServerError, nil)
	if _, err := c.Attestations(context.Background(), testRepo, sum); err == nil {
		t.Error("500: want an error")
	}
}

func TestRateLimitIsNamed(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "API rate limit exceeded"})
	})
	_, err := c.ReleaseByTag(context.Background(), testRepo, "v1")
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded; API rate limit exceeded") {
		t.Errorf("err = %v, want the rate limit named", err)
	}
}

func TestUnparseableAndUnreachableResponses(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) })
	if _, err := c.ListReleases(context.Background(), testRepo); err == nil || !strings.Contains(err.Error(), "parsing response") {
		t.Errorf("err = %v, want a parse error", err)
	}

	c = New("token")
	c.SetEndpoints("http://127.0.0.1:1", "http://127.0.0.1:1")
	if _, err := c.ReleaseByTag(context.Background(), testRepo, "v1"); err == nil {
		t.Error("an unreachable host should fail")
	}
	if _, err := c.DownloadAsset(context.Background(), testRepo, 1); err == nil {
		t.Error("an unreachable host should fail a download")
	}
}

func TestSetEndpointsTrimsTrailingSlashes(t *testing.T) {
	c := New("t")
	c.SetEndpoints("https://api.example/", "https://up.example/")
	if c.api != "https://api.example" || c.upload != "https://up.example" {
		t.Errorf("api = %q, upload = %q", c.api, c.upload)
	}
}
