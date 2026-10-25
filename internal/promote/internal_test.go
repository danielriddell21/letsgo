package promote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/manifest"
)

func TestStableTagFor(t *testing.T) {
	tests := []struct {
		name    string
		rcTag   string
		prefix  string
		want    string
		wantErr string
	}{
		{name: "root module rc", rcTag: "v1.3.0-rc.1", prefix: "", want: "v1.3.0"},
		{name: "root module beta", rcTag: "v2.0.0-beta.2", prefix: "", want: "v2.0.0"},
		{name: "scoped module", rcTag: "services/api/v1.5.0-rc.1", prefix: "services/api/", want: "services/api/v1.5.0"},
		{name: "not a prerelease", rcTag: "v1.3.0", prefix: "", wantErr: "is not a prerelease"},
		{name: "outside the scope", rcTag: "v1.3.0-rc.1", prefix: "services/api/", wantErr: "not a version tag in this module's scope"},
		{name: "does not parse", rcTag: "not-a-version", prefix: "", wantErr: "not a version tag in this module's scope"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stableTagFor(tc.rcTag, tc.prefix)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("stableTagFor(%q, %q): %v", tc.rcTag, tc.prefix, err)
			}
			if got != tc.want {
				t.Errorf("stableTagFor(%q, %q) = %q, want %q", tc.rcTag, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestPrereleasesOf(t *testing.T) {
	scope := discover.Scope{}
	tags := []string{
		"v1.2.0",        // an earlier stable release
		"v1.3.0-rc.1",   // the target's own first RC
		"v1.3.0-rc.2",   // the target's own second RC
		"v1.3.0-beta.1", // a differently-labelled prerelease of the same target
		"v1.4.0-rc.1",   // a prerelease of a different version entirely
		"v1.3.0",        // the stable tag itself, not a prerelease
	}
	target, ok := semver.Parse("1.3.0")
	if !ok {
		t.Fatal("test fixture does not parse")
	}

	got := prereleasesOf(tags, scope, target)

	want := []string{"v1.3.0-rc.2", "v1.3.0-rc.1", "v1.3.0-beta.1"}
	if len(got) != len(want) {
		t.Fatalf("prereleasesOf = %v, want %v", got, want)
	}
	// rc.2 must sort ahead of rc.1 (newest first); beta.1 must be included
	// as a prerelease of the same target even though its label differs.
	if got[0] != "v1.3.0-rc.2" || got[1] != "v1.3.0-rc.1" {
		t.Errorf("prereleasesOf = %v, want the two RCs newest first", got)
	}
	for _, tag := range got {
		if tag == "v1.3.0" || tag == "v1.2.0" || tag == "v1.4.0-rc.1" {
			t.Errorf("prereleasesOf included %s, which is not a prerelease of exactly 1.3.0", tag)
		}
	}
}

func TestPrereleasesOfNoneMatch(t *testing.T) {
	scope := discover.Scope{}
	target, _ := semver.Parse("2.0.0")
	got := prereleasesOf([]string{"v1.2.0", "v1.3.0-rc.1"}, scope, target)
	if len(got) != 0 {
		t.Errorf("prereleasesOf = %v, want none for a target with no prereleases", got)
	}
}

func TestRewriteManifestNamesWhatItCouldNotWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T) *release.Result
		wantErr string
	}{
		{
			name: "the release directory is missing",
			prepare: func(t *testing.T) *release.Result {
				t.Helper()
				return &release.Result{Dir: filepath.Join(t.TempDir(), "gone"), Manifest: &manifest.Manifest{}}
			},
			wantErr: "promote: release: writing the manifest",
		},
		{
			name: "a listed file is missing",
			prepare: func(t *testing.T) *release.Result {
				t.Helper()
				return &release.Result{Dir: t.TempDir(), Manifest: &manifest.Manifest{}, Files: []string{"absent.tar.gz"}}
			},
			wantErr: "promote: release: open",
		},
		{
			name: "the checksum file cannot be replaced",
			prepare: func(t *testing.T) *release.Result {
				t.Helper()
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, build.ChecksumFile), 0o700); err != nil {
					t.Fatal(err)
				}
				return &release.Result{Dir: dir, Manifest: &manifest.Manifest{}}
			},
			wantErr: "promote: release: writing " + build.ChecksumFile,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := rewriteManifest(tc.prepare(t))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("rewriteManifest error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestManifestDigestNamesAMissingManifest(t *testing.T) {
	t.Parallel()

	_, err := manifestDigest(&release.Result{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "promote: hashing the manifest") {
		t.Errorf("manifestDigest error = %v, want it to name the manifest", err)
	}
}

func TestFindReleaseNamesTheListingItCouldNotRead(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := findRelease(context.Background(), Options{Client: client}, "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "promote: listing releases") {
		t.Errorf("findRelease error = %v, want it to name the listing", err)
	}
}

func TestBuildNotesNamesWhatItCouldNotCollect(t *testing.T) {
	t.Parallel()

	_, err := buildNotes(context.Background(), Options{Dir: t.TempDir(), GitBin: "git"}, "v1.0.0", &plan.Plan{}, &release.Result{})
	if err == nil || !strings.Contains(err.Error(), "promote: collecting the commits") {
		t.Errorf("buildNotes error = %v, want it to name the commits", err)
	}
}

func TestBuildNotesNamesTheManifestItCouldNotDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "feat: first"},
		{"tag", "v1.0.0"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	o := Options{Dir: dir, GitBin: "git", Shallow: true}
	_, err := buildNotes(context.Background(), o, "v1.0.0", &plan.Plan{}, &release.Result{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "promote: digesting the manifest") {
		t.Errorf("buildNotes error = %v, want it to name the manifest digest", err)
	}
}

// tagRepo is a repository with one commit, and an origin that refuses every
// push: it does not exist.
func tagRepo(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "feat: first"},
		{"remote", "add", "origin", filepath.Join(t.TempDir(), "absent.git")},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

// When the push is refused, as GitHub refuses a tag whose workflows differ
// from the default branch's, the tag goes on the forge through the API, the
// same annotated tag, and the release is told it is there.
func TestTagCommitFallsBackToTheAPIWhenThePushIsRefused(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "Champ")
	t.Setenv("GIT_COMMITTER_EMAIL", "champ@example.com")
	dir, commit := tagRepo(t)

	var object map[string]any
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/repos/you/foo/git/tags" {
			_ = json.NewDecoder(r.Body).Decode(&object)
			_, _ = w.Write([]byte(`{"sha":"t4g"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	o := Options{Dir: dir, GitBin: "git", Client: client, Repo: github.Repo{Owner: "you", Name: "foo"}}

	onForge, err := tagCommit(context.Background(), o, "v1.0.0", commit, logf)
	if err != nil || !onForge {
		t.Fatalf("tagCommit = %v, %v, want the tag on the forge", onForge, err)
	}
	if !slices.Equal(paths, []string{"/repos/you/foo/git/tags", "/repos/you/foo/git/refs"}) {
		t.Errorf("API requests = %v", paths)
	}
	if tagger, _ := object["tagger"].(map[string]any); object["object"] != commit || tagger["name"] != "Champ" {
		t.Errorf("tag object = %v, want the local tag's commit and tagger", object)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "created it through the API instead") {
		t.Errorf("logs = %q", logs)
	}
}

// With neither the push nor the API, the tag is left to the release, and the
// release is told so it can hold the gate until the tag exists.
func TestTagCommitLeavesTheTagToTheReleaseWhenTheAPIRefusesToo(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "Champ")
	t.Setenv("GIT_COMMITTER_EMAIL", "champ@example.com")
	dir, commit := tagRepo(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	for _, o := range []Options{
		{Dir: dir, GitBin: "git", Client: client, Repo: github.Repo{Owner: "you", Name: "foo"}},
		{Dir: dir, GitBin: "git"}, // no forge client at all
	} {
		var logs []string
		logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
		onForge, err := tagCommit(context.Background(), o, "v1.0.0", commit, logf)
		if err != nil || onForge {
			t.Fatalf("tagCommit = %v, %v, want the tag left to the release", onForge, err)
		}
		if last := logs[len(logs)-1]; !strings.Contains(last, "the release in step 4 will create it there") {
			t.Errorf("logs = %q", logs)
		}
	}
}
