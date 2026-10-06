package githubsource_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/releases/githubsource"
)

func newSource(t *testing.T) *githubsource.Source {
	t.Helper()
	rel := github.Release{
		ID: 7, TagName: "v1.2.3", Name: "v1.2.3", Body: "notes",
		Draft: true, Prerelease: true, Immutable: true,
		Assets: []github.Asset{
			{ID: 1, Name: "a.tgz", Size: 10, Digest: "sha256:abc"},
			{ID: 2, Name: "b.tgz", Size: 20},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/you/demo/releases/tags/v1.2.3", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/repos/you/demo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/repos/you/demo/releases/assets/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("payload"))
	})
	mux.HandleFunc("/repos/you/demo/releases", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]github.Release{rel})
	})
	mux.HandleFunc("/repos/you/demo/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "v1.2.3"}, {Name: "v1.2.2"}})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return &githubsource.Source{Client: client, Repo: github.Repo{Owner: "you", Name: "demo"}}
}

func TestSourceConvertsReleases(t *testing.T) {
	src := newSource(t)
	ctx := context.Background()

	byTag, err := src.ReleaseByTag(ctx, "v1.2.3")
	if err != nil || byTag == nil {
		t.Fatalf("ReleaseByTag = %v, %v", byTag, err)
	}
	if byTag.ID != 7 || byTag.Tag != "v1.2.3" || byTag.Body != "notes" ||
		!byTag.Draft || !byTag.Prerelease || !byTag.Immutable {
		t.Errorf("ReleaseByTag = %+v", byTag)
	}
	if len(byTag.Assets) != 2 || byTag.Assets[0].SHA256 != "abc" || byTag.Assets[0].Size != 10 || byTag.Assets[1].SHA256 != "" {
		t.Errorf("assets = %+v", byTag.Assets)
	}

	latest, err := src.LatestRelease(ctx)
	if err != nil || latest == nil || latest.Tag != "v1.2.3" {
		t.Errorf("LatestRelease = %+v, %v", latest, err)
	}

	all, err := src.ListReleases(ctx)
	if err != nil || len(all) != 1 || all[0].Tag != "v1.2.3" {
		t.Errorf("ListReleases = %+v, %v", all, err)
	}
}

func TestSourceTagsAndDownload(t *testing.T) {
	src := newSource(t)
	ctx := context.Background()

	tags, err := src.Tags(ctx, 10)
	if err != nil || !reflect.DeepEqual(tags, []string{"v1.2.3", "v1.2.2"}) {
		t.Errorf("Tags = %v, %v", tags, err)
	}

	data, err := src.DownloadAsset(ctx, releases.Asset{ID: 1})
	if err != nil || string(data) != "payload" {
		t.Errorf("DownloadAsset = %q, %v", data, err)
	}
}

func TestSourceReportsMissingReleaseAsNil(t *testing.T) {
	src := newSource(t)
	got, err := src.ReleaseByTag(context.Background(), "v9.9.9")
	if err != nil || got != nil {
		t.Errorf("ReleaseByTag(missing) = %v, %v; want nil, nil", got, err)
	}
}
