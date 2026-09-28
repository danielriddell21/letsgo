package promote_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/promote"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
)

const mainGo = `package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("demo %s (%s) built %s\n", version, commit, date)
	}
}
`

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// gitInit commits every file already written under dir as one commit, with a
// fixed author/committer identity so the build is reproducible, then applies
// each tag to that commit.
func gitInit(t *testing.T, dir string, tags ...string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "feat: first")
	for _, tag := range tags {
		run("tag", "-a", tag, "-m", tag)
	}
}

// buildRC builds a real prerelease into dist, from the repository at dir
// (which must already carry rcTag on HEAD).
func buildRC(t *testing.T, dir string) *release.Result {
	t.Helper()
	p, err := plan.Resolve(context.Background(), plan.Options{Dir: dir})
	if err != nil || !p.OK() {
		t.Fatalf("plan: %v %+v", err, p.Checks)
	}
	dist := t.TempDir()
	result, err := release.Build(context.Background(), p, dist, "test", nil, nil)
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	return result
}

// fakeAsset is a file attached to a fakeRelease.
type fakeAsset struct {
	id   int64
	name string
	data []byte
}

// fakeRelease is enough of a GitHub release for promote's own calls.
type fakeRelease struct {
	id         int64
	tagName    string
	name       string
	body       string
	draft      bool
	prerelease bool
	assets     []*fakeAsset
}

// fakeForge is a minimal, in-memory GitHub releases API: just the endpoints
// internal/promote and internal/publish call, nothing more.
type fakeForge struct {
	t    *testing.T
	repo string // "owner/name"

	mu           sync.Mutex
	releases     []*fakeRelease
	nextAssetID  int64
	updateCalls  int
	createCalls  int
	lastCreate   github.ReleaseInput
	lastCreateOK bool
}

func newFakeForge(t *testing.T, repo string) *fakeForge {
	return &fakeForge{t: t, repo: repo, nextAssetID: 1000}
}

func (f *fakeForge) server() *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(f.route))
	f.t.Cleanup(server.Close)
	return server
}

func (f *fakeForge) client() *github.Client {
	server := f.server()
	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return client
}

func segment(path string, fromEnd int) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	idx := len(segs) - fromEnd
	if idx < 0 || idx >= len(segs) {
		return ""
	}
	return segs[idx]
}

func (f *fakeForge) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/releases"):
		f.listReleases(w)

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/releases"):
		f.createRelease(w, r)

	case r.Method == http.MethodGet && strings.Contains(path, "/releases/tags/"):
		f.releaseByTag(w, segment(path, 1))

	case r.Method == http.MethodPatch:
		f.updateRelease(w, r, pathInt(path, 1))

	case r.Method == http.MethodGet && strings.Contains(path, "/releases/assets/"):
		f.downloadAsset(w, pathInt(path, 1))

	case r.Method == http.MethodGet && strings.HasSuffix(path, "/assets"):
		f.listAssets(w, pathInt(path, 2))

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/assets"):
		f.uploadAsset(w, r, pathInt(path, 2), r.URL.Query().Get("name"))

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func pathInt(path string, fromEnd int) int64 {
	n, _ := strconv.ParseInt(segment(path, fromEnd), 10, 64)
	return n
}

func (f *fakeRelease) toGithub() github.Release {
	assets := make([]github.Asset, 0, len(f.assets))
	for _, a := range f.assets {
		sum := sha256.Sum256(a.data)
		assets = append(assets, github.Asset{
			ID: a.id, Name: a.name, Size: int64(len(a.data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	return github.Release{
		ID: f.id, TagName: f.tagName, Name: f.name, Body: f.body,
		Draft: f.draft, Prerelease: f.prerelease, Assets: assets,
	}
}

func (f *fakeForge) listReleases(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]github.Release, 0, len(f.releases))
	for _, r := range f.releases {
		out = append(out, r.toGithub())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeForge) releaseByTag(w http.ResponseWriter, tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.releases {
		if r.tagName == tag {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(r.toGithub())
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeForge) createRelease(w http.ResponseWriter, r *http.Request) {
	var in github.ReleaseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.createCalls++
	f.lastCreate, f.lastCreateOK = in, true
	id := int64(len(f.releases) + 1)
	rel := &fakeRelease{id: id, tagName: in.TagName, name: in.Name, body: in.Body, draft: in.Draft, prerelease: in.Prerelease}
	f.releases = append(f.releases, rel)
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rel.toGithub())
}

func (f *fakeForge) updateRelease(w http.ResponseWriter, r *http.Request, id int64) {
	var in github.ReleaseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	for _, rel := range f.releases {
		if rel.id == id {
			rel.tagName, rel.name, rel.body = in.TagName, in.Name, in.Body
			rel.draft, rel.prerelease = in.Draft, in.Prerelease
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel.toGithub())
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeForge) downloadAsset(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rel := range f.releases {
		for _, a := range rel.assets {
			if a.id == id {
				_, _ = w.Write(a.data)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeForge) listAssets(w http.ResponseWriter, releaseID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rel := range f.releases {
		if rel.id == releaseID {
			out := make([]github.Asset, 0, len(rel.assets))
			for _, a := range rel.assets {
				sum := sha256.Sum256(a.data)
				out = append(out, github.Asset{
					ID: a.id, Name: a.name, Size: int64(len(a.data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeForge) uploadAsset(w http.ResponseWriter, r *http.Request, releaseID int64, name string) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rel := range f.releases {
		if rel.id == releaseID {
			f.nextAssetID++
			asset := &fakeAsset{id: f.nextAssetID, name: name, data: data}
			rel.assets = append(rel.assets, asset)
			sum := sha256.Sum256(data)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Asset{
				ID: asset.id, Name: name, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
			})
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

// seedRC adds an RC release carrying only its manifest as an asset — the one
// file promote itself ever downloads from the RC.
func (f *fakeForge) seedRC(tag string, manifestData []byte) *fakeRelease {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextAssetID++
	rel := &fakeRelease{
		id: int64(len(f.releases) + 1), tagName: tag, name: tag, prerelease: true,
		assets: []*fakeAsset{{id: f.nextAssetID, name: manifest.FileName, data: manifestData}},
	}
	f.releases = append(f.releases, rel)
	return rel
}

// The whole loop: build a real RC, publish it through the fake forge, promote
// it, and check the stable release the pipeline produced.
func TestRunPromotesACleanRC(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    mainGo,
		"README.md":  "# demo\n",
		"letsgo.mod": "build " + gobuild.Host().String() + "\n",
	})
	gitInit(t, dir, "v1.3.0-rc.1")

	rc := buildRC(t, dir)
	manifestData, err := os.ReadFile(filepath.Join(rc.Dir, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}

	forge := newFakeForge(t, "you/demo")
	forge.seedRC("v1.3.0-rc.1", manifestData)
	client := forge.client()

	result, err := promote.Run(context.Background(), promote.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		RCTag: "v1.3.0-rc.1", Dir: dir, ModuleDir: dir,
		ToolVersion: "test", WorkDir: t.TempDir(), Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("promote.Run: %v", err)
	}

	if result.StableTag != "v1.3.0" {
		t.Errorf("StableTag = %q, want v1.3.0", result.StableTag)
	}
	if !result.RC.Prerelease {
		t.Error("the RC's own release was not restored to a prerelease")
	}
	if result.Published.Release.TagName != "v1.3.0" {
		t.Errorf("published tag = %q, want v1.3.0", result.Published.Release.TagName)
	}
	if result.Published.Release.Prerelease {
		t.Error("the stable release was published as a prerelease")
	}
	if result.Build.Manifest.PromotedFrom == nil || result.Build.Manifest.PromotedFrom.Tag != "v1.3.0-rc.1" {
		t.Errorf("PromotedFrom = %+v, want tag v1.3.0-rc.1", result.Build.Manifest.PromotedFrom)
	}

	forge.mu.Lock()
	defer forge.mu.Unlock()
	if forge.updateCalls != 1 {
		t.Errorf("UpdateRelease was called %d times, want exactly 1 (restoring the RC)", forge.updateCalls)
	}
	if forge.createCalls != 1 {
		t.Errorf("CreateRelease was called %d times, want exactly 1", forge.createCalls)
	}
	if !forge.lastCreateOK || forge.lastCreate.MakeLatest != "true" {
		t.Errorf("the stable release was not created with make_latest=true: %+v", forge.lastCreate)
	}
	if forge.lastCreate.TargetCommitish != rc.Manifest.Commit {
		t.Errorf("target_commitish = %q, want the RC's own commit %q", forge.lastCreate.TargetCommitish, rc.Manifest.Commit)
	}
}

// A rebuild that does not match the RC's own manifest must fail loudly and
// must not create the stable release — the RC's release is still restored
// first, because that step is unconditional.
func TestRunRefusesAMismatchedRebuild(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    mainGo,
		"README.md":  "# demo\n",
		"letsgo.mod": "build " + gobuild.Host().String() + "\n",
	})
	gitInit(t, dir, "v1.3.0-rc.1")

	rc := buildRC(t, dir)
	m, err := manifest.Decode(mustRead(t, filepath.Join(rc.Dir, manifest.FileName)))
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with a field Compare checks, so the RC's own manifest can never
	// agree with what gets rebuilt from the very same commit.
	m.Builder.Go = "go9.9.9"
	tampered, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}

	forge := newFakeForge(t, "you/demo")
	forge.seedRC("v1.3.0-rc.1", tampered)
	client := forge.client()

	_, err = promote.Run(context.Background(), promote.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		RCTag: "v1.3.0-rc.1", Dir: dir, ModuleDir: dir,
		ToolVersion: "test", WorkDir: t.TempDir(), Logf: t.Logf,
	})
	if err == nil {
		t.Fatal("a rebuild that disagrees with the RC's manifest was promoted")
	}
	if !strings.Contains(err.Error(), "go version") {
		t.Errorf("error = %v, want it to name the go version difference", err)
	}

	forge.mu.Lock()
	defer forge.mu.Unlock()
	if forge.updateCalls != 1 {
		t.Errorf("UpdateRelease was called %d times, want exactly 1 (the RC is still restored first)", forge.updateCalls)
	}
	if forge.createCalls != 0 {
		t.Error("a stable release was created despite the mismatch")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Refusal conditions (PR-14): each must be caught before anything is written.
func TestRunRefusalConditions(t *testing.T) {
	tests := []struct {
		name  string
		rcTag string
		setup func(f *fakeForge)
		want  string
	}{
		{
			name:  "no such release",
			rcTag: "v1.3.0-rc.1",
			setup: func(f *fakeForge) {},
			want:  "has no release tagged",
		},
		{
			name:  "draft release",
			rcTag: "v1.3.0-rc.1",
			setup: func(f *fakeForge) {
				rel := f.seedRC("v1.3.0-rc.1", []byte(`{}`))
				rel.draft = true
			},
			want: "is a draft",
		},
		{
			name:  "yanked release",
			rcTag: "v1.3.0-rc.1",
			setup: func(f *fakeForge) {
				rel := f.seedRC("v1.3.0-rc.1", []byte(`{}`))
				rel.body = "> [!CAUTION]\n> **This release is retracted.**\n"
			},
			want: "is yanked",
		},
		{
			name:  "not a prerelease tag",
			rcTag: "v1.3.0",
			setup: func(f *fakeForge) {},
			want:  "is not a prerelease",
		},
		{
			name:  "target already exists",
			rcTag: "v1.3.0-rc.1",
			setup: func(f *fakeForge) {
				f.seedRC("v1.3.0-rc.1", []byte(`{}`))
				f.seedRC("v1.3.0", []byte(`{}`))
			},
			want: "already exists",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"go.mod":     "module example.com/demo\n\ngo 1.24\n",
				"main.go":    mainGo,
				"letsgo.mod": "build " + gobuild.Host().String() + "\n",
			})
			gitInit(t, dir, "v1.3.0-rc.1")

			forge := newFakeForge(t, "you/demo")
			tc.setup(forge)
			client := forge.client()

			_, err := promote.Run(context.Background(), promote.Options{
				Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
				RCTag: tc.rcTag, Dir: dir, ModuleDir: dir,
				ToolVersion: "test", WorkDir: t.TempDir(), Logf: t.Logf,
			})
			if err == nil {
				t.Fatal("expected a refusal, got no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}

			forge.mu.Lock()
			defer forge.mu.Unlock()
			if forge.updateCalls != 0 {
				t.Errorf("UpdateRelease was called %d times; a refusal must write nothing", forge.updateCalls)
			}
			if forge.createCalls != 0 {
				t.Errorf("CreateRelease was called %d times; a refusal must write nothing", forge.createCalls)
			}
		})
	}
}
