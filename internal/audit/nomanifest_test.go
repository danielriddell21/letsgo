package audit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/audit"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

func TestRunRejectsReleaseWithoutManifest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/you/demo/releases/tags/v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "v1.0.0"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := audit.Run(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.0.0", WorkDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "only releases published by letsgo can be verified") {
		t.Fatalf("err = %v, want a no-manifest error", err)
	}
}
