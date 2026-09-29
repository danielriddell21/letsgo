package audit_test

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
	"runtime"
	"strconv"
	"strings"
	"sync"
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

// served is one asset the fake forge is currently holding, keyed by ID.
// audit.json starts absent and is added/replaced/removed by the same
// upload/delete calls a real release-time publish or a later audit uses,
// so the fake has to track this state rather than serve it statically.
type served struct {
	name string
	data []byte
}

// serve exposes the built release through enough of the API for audit: the
// tag lookup, asset downloads, and asset upload/delete (so a second
// audit.Run against the same server sees the first run's audit.json).
// tamper serves corrupted bytes for the named asset, so its digest no
// longer matches what the manifest recorded — the "tampered source"
// acceptance scenario. audit checks a downloaded asset against the
// manifest's own digest, not against what the forge reports for it, so
// corrupting only the reported digest (as verify's own fixture does) would
// not exercise this path.
func (p *published) serve(t *testing.T, tamper string) *github.Client {
	t.Helper()

	var mu sync.Mutex
	byID := map[int64]served{}
	nextID := int64(100)
	for _, name := range p.result.Files {
		data, err := os.ReadFile(filepath.Join(p.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		nextID++
		byID[nextID] = served{name: name, data: data}
	}

	assetsLocked := func() []github.Asset {
		var assets []github.Asset
		for id, s := range byID {
			assets = append(assets, github.Asset{ID: id, Name: s.name, Size: int64(len(s.data)), Digest: "sha256:" + sha256Hex(s.data)})
		}
		return assets
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			if !strings.HasSuffix(r.URL.Path, "/v1.2.3") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "v1.2.3", Assets: assetsLocked()})

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/releases/") && strings.HasSuffix(r.URL.Path, "/assets"):
			name := r.URL.Query().Get("name")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			nextID++
			byID[nextID] = served{name: name, data: data}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Asset{ID: nextID, Name: name, Size: int64(len(data)), Digest: "sha256:" + sha256Hex(data)})

		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			delete(byID, assetID)
			w.WriteHeader(http.StatusNoContent)

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			s, ok := byID[assetID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if s.name == tamper {
				_, _ = w.Write([]byte("not the real archive"))
				return
			}
			_, _ = w.Write(s.data)

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
	if !result.Recorded {
		t.Error("Recorded = false, want true for a release's first audit")
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
	if !result.Recorded {
		t.Error("Recorded = false, want true for a release's first audit")
	}
}

// A second run against an unchanged vulndb (same date, same findings) must
// not append a new entry (AU-6), and must not touch any asset other than
// audit.json (AU-8).
func TestRunSkipsAnUnchangedRerun(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client := p.serve(t, "")
	opts := audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3",
	}

	opts.WorkDir = t.TempDir()
	first, err := audit.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("first audit.Run: %v", err)
	}
	if !first.Recorded {
		t.Fatal("first run was not recorded")
	}

	opts.WorkDir = t.TempDir()
	second, err := audit.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("second audit.Run: %v", err)
	}
	if second.Recorded {
		t.Error("second run recorded a duplicate entry, want it skipped")
	}

	record := fetchRecord(t, client)
	if len(record.Audits) != 1 {
		t.Fatalf("audits = %+v, want exactly 1", record.Audits)
	}

	release, err := client.ReleaseByTag(context.Background(), github.Repo{Owner: "you", Name: "demo"}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Assets) != len(p.result.Files)+1 {
		t.Fatalf("release has %d assets, want the %d built files plus %s",
			len(release.Assets), len(p.result.Files), audit.FileName)
	}
	for _, name := range p.result.Files {
		asset, ok := release.Asset(name)
		if !ok {
			t.Fatalf("release lost its %s asset", name)
		}
		data, err := os.ReadFile(filepath.Join(p.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := asset.SHA256(); got != sha256Hex(data) {
			t.Errorf("%s digest changed: got %s, want %s", name, got, sha256Hex(data))
		}
	}
}

// A run against a changed vulndb (a new database date, even with the same
// findings) must append, keeping the earlier entry (AU-5).
func TestRunAppendsWhenTheVulndbChanges(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client := p.serve(t, "")
	opts := audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3",
	}

	opts.WorkDir = t.TempDir()
	if _, err := audit.Run(context.Background(), opts); err != nil {
		t.Fatalf("first audit.Run: %v", err)
	}

	const laterOutput = `{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-24T00:00:00Z"}}`
	fakeGovulncheck(t, laterOutput)
	opts.WorkDir = t.TempDir()
	second, err := audit.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("second audit.Run: %v", err)
	}
	if !second.Recorded {
		t.Error("Recorded = false, want true when the vulndb date changed")
	}

	record := fetchRecord(t, client)
	if len(record.Audits) != 2 {
		t.Fatalf("audits = %+v, want exactly 2", record.Audits)
	}
	if record.Audits[0].Vulndb != "2026-09-23" || record.Audits[1].Vulndb != "2026-09-24" {
		t.Errorf("audits = %+v", record.Audits)
	}
}

// fetchRecord reads back the audit.json the fake forge is currently
// holding for v1.2.3, the same way a later audit.Run would.
func fetchRecord(t *testing.T, client *github.Client) audit.Record {
	t.Helper()
	release, err := client.ReleaseByTag(context.Background(), github.Repo{Owner: "you", Name: "demo"}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := release.Asset(audit.FileName)
	if !ok {
		t.Fatalf("release has no %s", audit.FileName)
	}
	data, err := client.DownloadAsset(context.Background(), github.Repo{Owner: "you", Name: "demo"}, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	var record audit.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
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
