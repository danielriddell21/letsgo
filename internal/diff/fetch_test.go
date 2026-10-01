package diff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

func TestFetchErrors(t *testing.T) {
	repo := github.Repo{Owner: "you", Name: "demo"}
	bare := github.Release{ID: 1, TagName: "v1.0.0"}

	tests := []struct {
		name    string
		tag     string
		status  int
		release any
		want    string
	}{
		{name: "latest lookup fails", status: 500, want: "500"},
		{name: "no releases", status: 404, want: "has no releases"},
		{name: "no such tag", tag: "v9.9.9", status: 404, want: "has no release tagged v9.9.9"},
		{name: "no manifest", tag: "v1.0.0", status: 200, release: bare, want: "only releases published by letsgo can be diffed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if tc.release != nil {
					_ = json.NewEncoder(w).Encode(tc.release)
				}
			}))
			defer server.Close()
			c := github.New("token")
			c.SetEndpoints(server.URL, server.URL)

			_, err := Fetch(context.Background(), c, repo, discover.Scope{}, tc.tag)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// In a monorepo the repository-wide latest release can belong to another
// module, so the default "to" is the latest within the module's own scope.
func TestFetchDefaultsToTheLatestReleaseInScope(t *testing.T) {
	repo := github.Repo{Owner: "you", Name: "demo"}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/you/demo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "other/v9.0.0"})
	})
	mux.HandleFunc("/repos/you/demo/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "other/v9.0.0"}, {Name: "svc/v1.0.0"}})
	})
	mux.HandleFunc("/repos/you/demo/releases/tags/svc/v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{
			ID: 2, TagName: "svc/v1.0.0",
			Assets: []github.Asset{{ID: 5, Name: manifest.FileName}},
		})
	})
	mux.HandleFunc("/repos/you/demo/releases/assets/5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schema":1,"version":"v1.0.0"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	c := github.New("token")
	c.SetEndpoints(server.URL, server.URL)

	m, err := Fetch(context.Background(), c, repo, discover.Scope{Prefix: "svc/"}, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if m.Version != "v1.0.0" {
		t.Errorf("Version = %q, want the svc/ module's release", m.Version)
	}
}
