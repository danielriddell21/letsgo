package promote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/manifest"
)

func failingOptions(t *testing.T) Options {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	c := github.New("token")
	c.SetEndpoints(server.URL, server.URL)
	return Options{Client: c, Repo: github.Repo{Owner: "you", Name: "demo"}}
}

func TestFetchManifestErrors(t *testing.T) {
	o := failingOptions(t)

	_, _, err := fetchManifest(context.Background(), o, &releases.Published{Tag: "v1.0.0-rc.1"})
	if err == nil || !strings.Contains(err.Error(), "only a release letsgo published can be promoted") {
		t.Errorf("no manifest: err = %v", err)
	}

	rc := &releases.Published{Tag: "v1.0.0-rc.1", Assets: []releases.Asset{{ID: 1, Name: manifest.FileName}}}
	_, _, err = fetchManifest(context.Background(), o, rc)
	if err == nil || !strings.Contains(err.Error(), "reading v1.0.0-rc.1's manifest") {
		t.Errorf("read failure: err = %v", err)
	}
}

func TestRestoreRCError(t *testing.T) {
	_, err := restoreRC(context.Background(), failingOptions(t), &releases.Published{ID: 1, Tag: "v1.0.0-rc.1"})
	if err == nil || !strings.Contains(err.Error(), "promote: restoring v1.0.0-rc.1") {
		t.Errorf("err = %v", err)
	}
}
