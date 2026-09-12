package verify_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/verify"
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

// published is a release that actually exists: built by the real pipeline,
// then served back so verification has something genuine to check.
type published struct {
	dir    string // the repository
	dist   string // the built artifacts
	result *release.Result
	assets map[string]int64 // name -> id
}

func buildRelease(t *testing.T) *published {
	t.Helper()
	dir := t.TempDir()

	write := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/demo\n\ngo 1.24\n")
	write("main.go", mainGo)
	write("README.md", "# demo\n")
	write("letsgo.mod", "build "+gobuild.Host().String()+"\n")

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

	// Deliberately hostile, and deliberately not corrected with
	// .gitattributes: this is git's default on Windows, and it rewrites line
	// endings on checkout. A rebuild from such a checkout compiles different
	// source than the release did, so verification fails with no defect behind
	// it. Configuring it here exercises the guard on every platform rather
	// than only on the one where it bites.
	run("config", "core.autocrlf", "true")

	run("add", ".")
	run("commit", "-q", "-m", "feat: first")
	run("tag", "v1.2.3")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: dir})
	if err != nil || !p.OK() {
		t.Fatalf("plan: %v %+v", err, p.Checks)
	}

	dist := t.TempDir()
	result, err := release.Build(context.Background(), p, dist, "test", nil)
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	return &published{dir: dir, dist: dist, result: result, assets: map[string]int64{}}
}

// serve exposes the built release through enough of the API for verification.
// corrupt names an asset whose reported digest should be wrong.
func (p *published) serve(t *testing.T, corrupt string) *github.Client {
	t.Helper()

	var assets []github.Asset
	var id int64 = 100
	for _, name := range p.result.Files {
		data, err := os.ReadFile(filepath.Join(p.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256Of(data)
		if name == corrupt {
			digest = strings.Repeat("0", 64)
		}
		id++
		p.assets[name] = id
		assets = append(assets, github.Asset{
			ID: id, Name: name, Size: int64(len(data)), Digest: "sha256:" + digest,
		})
	}

	byID := map[int64]string{}
	for name, assetID := range p.assets {
		byID[assetID] = name
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/") || strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Release{
				ID: 1, TagName: "v1.2.3", Assets: assets,
			})

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for assetID, name := range byID {
				if idText == itoa(assetID) {
					data, err := os.ReadFile(filepath.Join(p.dist, name))
					if err != nil {
						t.Error(err)
					}
					_, _ = w.Write(data)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)

		case strings.Contains(r.URL.Path, "/attestations/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"attestations": []any{}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return client
}

func run(t *testing.T, p *published, o verify.Options) *verify.Result {
	t.Helper()
	if o.Client == nil {
		o.Client = p.serve(t, "")
	}
	o.Repo = github.Repo{Owner: "you", Name: "demo"}
	o.WorkDir = t.TempDir()

	result, err := verify.Run(context.Background(), o)
	if err != nil {
		t.Fatalf("verify.Run: %v", err)
	}
	return result
}

func find(t *testing.T, r *verify.Result, name string) verify.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, r.Checks)
	return verify.Check{}
}

// The whole loop: build a release, publish it, rebuild it, and find the same
// bytes. If this does not hold, nothing else in the tool means very much.
func TestVerifyReproducesARealRelease(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir})

	if !result.OK() {
		t.Fatalf("a freshly built release did not verify: %+v", result.Checks)
	}
	if c := find(t, result, "rebuild"); c.Status != verify.Pass {
		t.Errorf("rebuild = %+v", c)
	}
	if !strings.Contains(result.SourceFrom, "local checkout") {
		t.Errorf("SourceFrom = %q, want the local checkout", result.SourceFrom)
	}
}

// Without a checkout the release's own source archive is used, and the report
// must say so: it establishes internal consistency, not provenance from a
// repository anyone can read.
func TestVerifyFallsBackToThePublishedSource(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3"}) // no Dir

	if !result.OK() {
		t.Fatalf("verification failed: %+v", result.Checks)
	}
	if !strings.Contains(result.SourceFrom, "source archive") {
		t.Errorf("SourceFrom = %q, want the published source archive", result.SourceFrom)
	}
}

// An asset that does not match what the release describes is the case this
// exists to catch.
func TestVerifyDetectsATamperedAsset(t *testing.T) {
	p := buildRelease(t)
	corrupted := p.result.Artifacts[0].Archive

	result := run(t, p, verify.Options{
		Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true,
		Client: p.serve(t, corrupted),
	})

	if result.OK() {
		t.Fatal("a mismatched asset digest verified")
	}
	c := find(t, result, "published assets")
	if c.Status != verify.Fail || !strings.Contains(c.Detail, corrupted) {
		t.Errorf("published assets = %+v, want a failure naming %s", c, corrupted)
	}
}

func TestVerifySkipsRebuildWhenAsked(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	if c := find(t, result, "rebuild"); c.Status != verify.Skip {
		t.Errorf("rebuild = %+v, want skip", c)
	}
	if !result.OK() {
		t.Errorf("skipping the rebuild should not fail verification: %+v", result.Checks)
	}
}

// A release with no provenance is still verifiable; it carries one guarantee
// fewer, which is worth reporting and not worth failing.
func TestVerifyReportsMissingProvenanceWithoutFailing(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	if c := find(t, result, "provenance"); c.Status != verify.Warn {
		t.Errorf("provenance = %+v, want warn", c)
	}
	if !result.OK() {
		t.Error("missing provenance failed the verification")
	}
}

// Only releases that published a manifest can be verified, and the reason
// has to be legible.
func TestVerifyNeedsAManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "v9.9.9"})
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := verify.Run(context.Background(), verify.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v9.9.9", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a release with no manifest verified")
	}
	if !strings.Contains(err.Error(), manifest.FileName) {
		t.Errorf("error does not explain what is missing: %v", err)
	}
}

func TestVerifyRejectsAnUnknownSchema(t *testing.T) {
	if _, err := manifest.Decode([]byte(`{"schema":99}`)); err == nil {
		t.Error("an unknown schema was accepted")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
