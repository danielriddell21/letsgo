package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	c := New("token")
	c.SetEndpoints(server.URL, server.URL)
	return c
}

func TestCanCreateRelease(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		want    bool
		wantErr error
	}{
		{"forbidden means denied", http.StatusForbidden, false, nil},
		{"unauthorized means denied", http.StatusUnauthorized, false, nil},
		{"unprocessable means allowed", http.StatusUnprocessableEntity, true, nil},
		{"bad request means allowed", http.StatusBadRequest, true, nil},
		{"server error is indeterminate", http.StatusInternalServerError, false, ErrIndeterminate},
		{"not found is indeterminate", http.StatusNotFound, false, ErrIndeterminate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "probe"})
			})

			got, err := c.CanCreateRelease(context.Background(), Repo{Owner: "you", Name: "foo"})
			if got != tt.want {
				t.Errorf("CanCreateRelease = %v, want %v", got, tt.want)
			}
			switch {
			case tt.wantErr == nil && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The probe must be incapable of creating anything, whatever the forge does
// with it.
func TestCanCreateReleaseSendsAnUncreatableRequest(t *testing.T) {
	var body []byte
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	if _, err := c.CanCreateRelease(context.Background(), Repo{Owner: "you", Name: "foo"}); err != nil {
		t.Fatal(err)
	}

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("probe body is not JSON: %s", body)
	}
	if tag, ok := sent["tag_name"]; ok && tag != "" {
		t.Errorf("probe carried a usable tag_name %q; it could have created a release", tag)
	}
}

// A probe that succeeds has done the thing it exists to avoid doing.
func TestCanCreateReleaseReportsAnUnexpectedSuccess(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{ID: 1})
	})

	_, err := c.CanCreateRelease(context.Background(), Repo{Owner: "you", Name: "foo"})
	if err == nil {
		t.Fatal("a probe that created a release reported success")
	}
	if !strings.Contains(err.Error(), "unexpectedly succeeded") {
		t.Errorf("error does not describe what happened: %v", err)
	}
}

func TestCommitsUpToPaginates(t *testing.T) {
	var pages []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")

		// One full page, then a short one, which ends the walk.
		count := 100
		if r.URL.Query().Get("page") != "1" {
			count = 2
		}
		batch := make([]CommitInfo, count)
		for i := range batch {
			batch[i].SHA = "sha"
		}
		_ = json.NewEncoder(w).Encode(batch)
	})

	commits, err := c.CommitsUpTo(context.Background(), Repo{Owner: "you", Name: "foo"}, "v1.0.0")
	if err != nil {
		t.Fatalf("CommitsUpTo: %v", err)
	}
	if len(commits) != 102 {
		t.Errorf("got %d commits, want 102", len(commits))
	}
	if len(pages) != 2 {
		t.Errorf("requested pages %v, want two", pages)
	}
}

// An unbounded walk over a long history would spend a rate limit producing
// notes nobody reads.
func TestCommitsUpToIsBounded(t *testing.T) {
	requests := 0
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		batch := make([]CommitInfo, 100)
		_ = json.NewEncoder(w).Encode(batch)
	})

	if _, err := c.CommitsUpTo(context.Background(), Repo{Owner: "you", Name: "foo"}, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if requests != maxCommitPages {
		t.Errorf("made %d requests, want the %d-page cap", requests, maxCommitPages)
	}
}

func TestAssetSHA256(t *testing.T) {
	if hex, ok := (Asset{Digest: "sha256:abc"}).SHA256(); !ok || hex != "abc" {
		t.Errorf("SHA256() = %q, %v", hex, ok)
	}
	for _, digest := range []string{"", "sha256:", "md5:abc"} {
		if _, ok := (Asset{Digest: digest}).SHA256(); ok {
			t.Errorf("SHA256() accepted %q", digest)
		}
	}
}
