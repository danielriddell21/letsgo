package audit_test

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
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/audit"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plan"
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

// published is a release built by the real pipeline, so its source archive
// and manifest digest are exactly what audit has to check against — the
// same fixture shape internal/verify's tests use, trimmed to what audit
// itself reads (the tags and asset-download endpoints; no attestations).
type published struct {
	dist   string
	result *release.Result
}

func buildRelease(t *testing.T) *published {
	t.Helper()
	dir := t.TempDir()

	for name, content := range map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    mainGo,
		"letsgo.mod": "build " + gobuild.Host().String() + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

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
	run("tag", "v1.2.3")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: dir})
	if err != nil || !p.OK() {
		t.Fatalf("plan: %v %+v", err, p.Checks)
	}

	dist := t.TempDir()
	result, err := release.Build(context.Background(), p, dist, "test", nil, nil)
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	return &published{dist: dist, result: result}
}

// serve exposes the built release through enough of the API for audit: the
// tag lookup and asset downloads. tamper serves corrupted bytes for the
// named asset, so its digest no longer matches what the manifest recorded —
// the "tampered source" acceptance scenario. audit checks a downloaded
// asset against the manifest's own digest, not against what the forge
// reports for it, so corrupting only the reported digest (as verify's own
// fixture does) would not exercise this path.
func (p *published) serve(t *testing.T, tamper string) *github.Client {
	t.Helper()

	var assets []github.Asset
	byID := map[int64]string{}
	id := int64(100)
	for _, name := range p.result.Files {
		data, err := os.ReadFile(filepath.Join(p.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		id++
		byID[id] = name
		assets = append(assets, github.Asset{ID: id, Name: name, Size: int64(len(data)), Digest: "sha256:" + sha256Hex(data)})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			if !strings.HasSuffix(r.URL.Path, "/v1.2.3") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "v1.2.3", Assets: assets})

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			name, ok := byID[assetID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if name == tamper {
				_, _ = w.Write([]byte("not the real archive"))
				return
			}
			data, err := os.ReadFile(filepath.Join(p.dist, name))
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(data)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return client
}

// fakeGovulncheck puts a script named govulncheck ahead of PATH that prints
// out (govulncheck's own JSON stream) regardless of its arguments, so tests
// can drive audit.Run without a real vulnerability database.
func fakeGovulncheck(t *testing.T, out string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\n"
	path := filepath.Join(dir, "govulncheck")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const cleanOutput = `{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-23T00:00:00Z"}}`

const affectedOutput = `{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-23T00:00:00Z"}}
{"finding":{"osv":"GO-2026-1234","fixed_version":"v0.31.0","trace":[{"module":"golang.org/x/net","package":"golang.org/x/net/http2","function":"readFrame"}]}}`

func TestRunReportsACleanRelease(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)

	result, err := audit.Run(context.Background(), audit.Options{
		Client: p.serve(t, ""), Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.Run: %v", err)
	}
	if result.Entry.Status != audit.Clean {
		t.Errorf("status = %s, want clean", result.Entry.Status)
	}
	if result.Entry.Vulndb != "2026-09-23" || result.Entry.Govulncheck != "v1.1.4" {
		t.Errorf("entry = %+v", result.Entry)
	}
	if len(result.Entry.Findings) != 0 {
		t.Errorf("findings = %+v, want none", result.Entry.Findings)
	}
}

func TestRunReportsAnAffectedRelease(t *testing.T) {
	fakeGovulncheck(t, affectedOutput)
	p := buildRelease(t)

	result, err := audit.Run(context.Background(), audit.Options{
		Client: p.serve(t, ""), Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.Run: %v", err)
	}
	if result.Entry.Status != audit.Affected {
		t.Errorf("status = %s, want affected", result.Entry.Status)
	}
	if len(result.Entry.Findings) != 1 || result.Entry.Findings[0].ID != "GO-2026-1234" {
		t.Fatalf("findings = %+v", result.Entry.Findings)
	}
	if result.Entry.Findings[0].Module != "golang.org/x/net" || result.Entry.Findings[0].Fixed != "v0.31.0" {
		t.Errorf("finding = %+v", result.Entry.Findings[0])
	}
}

// A tampered source archive must fail before govulncheck ever runs: the
// scan would otherwise be of bytes the release never published.
func TestRunFailsOnATamperedSourceArchive(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)

	_, err := audit.Run(context.Background(), audit.Options{
		Client: p.serve(t, p.result.Manifest.Source.Archive),
		Repo:   github.Repo{Owner: "you", Name: "demo"},
		Tag:    "v1.2.3", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a tampered source archive was not rejected")
	}
}

func TestRunFailsForAnUnknownTag(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)

	_, err := audit.Run(context.Background(), audit.Options{
		Client: p.serve(t, ""), Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v9.9.9", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("an unknown tag was not rejected")
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
