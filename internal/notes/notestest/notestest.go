// Package notestest holds the fixtures the release-notes tests share: a
// repository with a history, and a forge that serves a manifest.
package notestest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/manifest"
)

// Forge serves one release whose only asset (when m is non-nil) is the
// manifest itself, reachable the way DownloadAsset actually fetches it: by
// numeric asset ID through the API host, not a browser_download_url.
func Forge(t *testing.T, repoName, tag string, m *manifest.Manifest) *github.Client {
	t.Helper()

	mux := http.NewServeMux()
	release := func(w http.ResponseWriter, _ *http.Request) {
		var assets []map[string]any
		if m != nil {
			assets = append(assets, map[string]any{"id": 1, "name": manifest.FileName})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": assets})
	}
	mux.HandleFunc("/repos/"+repoName+"/releases/tags/"+tag, release)
	mux.HandleFunc("/repos/"+repoName+"/releases/assets/1", func(w http.ResponseWriter, _ *http.Request) {
		data, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := github.New("")
	client.SetEndpoints(srv.URL, srv.URL)
	return client
}

// History writes a repository with two tags, so a local changelog Collect can
// resolve a real "previous" release without touching the network.
func History(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	identity := []string{"-c", "user.name=Test", "-c", "user.email=t@example.com"}
	run := func(args ...string) {
		t.Helper()
		full := append(append([]string{"-C", dir}, identity...), args...)
		cmd := exec.CommandContext(t.Context(), "git", full...) // NOSONAR: a test fixture runs the developer's own git
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", full, err, out)
		}
	}
	commit := func(name, message string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", message)
	}

	run("init", "-q", "-b", "main")
	commit("a", "feat: first release")
	run("tag", "v1.0.0")
	commit("b", "feat: second release")
	run("tag", "v1.1.0")

	return dir
}
