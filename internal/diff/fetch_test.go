package diff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/github"
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
