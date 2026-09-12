package changelog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

func repoWithHistory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Dan", "GIT_AUTHOR_EMAIL=d@example.com",
			"GIT_COMMITTER_NAME=Dan", "GIT_COMMITTER_EMAIL=d@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(name, message string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", message)
	}

	run("init", "-q", "-b", "main")
	commit("a", "feat: first")
	run("tag", "v1.0.0")
	commit("b", "fix: second")
	commit("c", "feat: third")
	run("tag", "v1.1.0")

	return dir
}

// firstReleaseRepo has exactly one tag, at HEAD: a genuine first release.
func firstReleaseRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Dan", "GIT_AUTHOR_EMAIL=d@example.com",
			"GIT_COMMITTER_NAME=Dan", "GIT_COMMITTER_EMAIL=d@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for _, c := range []struct{ name, message string }{
		{"a", "feat: first"},
		{"b", "fix: second"},
	} {
		if err := os.WriteFile(filepath.Join(dir, c.name), []byte(c.name), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", c.message)
	}
	run("tag", "v1.0.0")
	return dir
}

func TestCollectUsesLocalHistoryWhenComplete(t *testing.T) {
	dir := repoWithHistory(t)

	previous, commits, err := changelog.Collect(context.Background(), changelog.Source{
		Dir: dir, Tag: "v1.1.0",
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if previous != "v1.0.0" {
		t.Errorf("previous = %q, want v1.0.0", previous)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(commits), commits)
	}
	// Newest first, matching git log.
	if commits[0].Subject != "feat: third" {
		t.Errorf("commits[0] = %q, want the newest", commits[0].Subject)
	}
}

// The point of the fallback: a shallow checkout is a normal CI clone, not a
// mistake to be corrected with fetch-depth: 0.
func TestCollectFallsBackToTheForgeWhenShallow(t *testing.T) {
	var comparedBase, comparedHead string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tags"):
			// Deliberately not in version order, as the real API is not.
			_ = json.NewEncoder(w).Encode([]github.Tag{
				{Name: "v1.1.0"}, {Name: "v0.9.0"}, {Name: "v1.0.0"}, {Name: "nightly"},
			})
		case strings.Contains(r.URL.Path, "/compare/"):
			part := r.URL.Path[strings.Index(r.URL.Path, "/compare/")+len("/compare/"):]
			comparedBase, comparedHead, _ = strings.Cut(part, "...")

			var payload struct {
				Commits []github.CommitInfo `json:"commits"`
			}
			for _, c := range []struct{ sha, msg, name, login string }{
				{"aaa1111", "fix: second\n\ndetail", "Dan", "danielriddell21"},
				{"bbb2222", "feat: third", "Ada", ""},
			} {
				var info github.CommitInfo
				info.SHA = c.sha
				info.Commit.Message = c.msg
				info.Commit.Author.Name = c.name
				if c.login != "" {
					info.Author = &struct {
						Login string `json:"login"`
					}{Login: c.login}
				}
				payload.Commits = append(payload.Commits, info)
			}
			_ = json.NewEncoder(w).Encode(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	previous, commits, err := changelog.Collect(context.Background(), changelog.Source{
		Dir: t.TempDir(), Tag: "v1.1.0", Shallow: true,
		Client: client, Repo: github.Repo{Owner: "you", Name: "foo"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// The previous tag must be chosen by version order, not list order.
	if previous != "v1.0.0" {
		t.Errorf("previous = %q, want v1.0.0", previous)
	}
	if comparedBase != "v1.0.0" || comparedHead != "v1.1.0" {
		t.Errorf("compared %s...%s, want v1.0.0...v1.1.0", comparedBase, comparedHead)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	// The compare endpoint returns oldest first; the rest of the package
	// expects newest first.
	if commits[0].Subject != "feat: third" {
		t.Errorf("commits[0] = %q, want the newest first", commits[0].Subject)
	}
	if commits[1].Body != "detail" {
		t.Errorf("body not carried through: %q", commits[1].Body)
	}
	// A forge login identifies a contributor better than a git author name.
	if commits[1].Author != "danielriddell21" {
		t.Errorf("author = %q, want the login", commits[1].Author)
	}
}

// A first release has no earlier tag, and the compare endpoint needs a base.
// Reporting nothing would contradict the local path, which answers the same
// question with the whole history.
func TestCollectListsFullHistoryForAFirstRelease(t *testing.T) {
	var comparedCalled bool
	var commitsRef string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tags"):
			// Only the tag being released exists.
			_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "v1.0.0"}})
		case strings.Contains(r.URL.Path, "/compare/"):
			comparedCalled = true
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/commits"):
			commitsRef = r.URL.Query().Get("sha")
			var payload []github.CommitInfo
			for _, c := range []struct{ sha, msg string }{
				{"ccc3333", "feat: third"},
				{"bbb2222", "fix: second"},
				{"aaa1111", "feat: first"},
			} {
				var info github.CommitInfo
				info.SHA = c.sha
				info.Commit.Message = c.msg
				info.Commit.Author.Name = "Dan"
				payload = append(payload, info)
			}
			_ = json.NewEncoder(w).Encode(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	previous, commits, err := changelog.Collect(context.Background(), changelog.Source{
		Dir: t.TempDir(), Tag: "v1.0.0", Shallow: true,
		Client: client, Repo: github.Repo{Owner: "you", Name: "foo"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if previous != "" {
		t.Errorf("previous = %q, want empty for a first release", previous)
	}
	if comparedCalled {
		t.Error("compare was called with no base to compare against")
	}
	if commitsRef != "v1.0.0" {
		t.Errorf("listed commits up to %q, want v1.0.0", commitsRef)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want the whole history", len(commits))
	}
	// The commits endpoint already reports newest first, as git log does.
	if commits[0].Subject != "feat: third" {
		t.Errorf("commits[0] = %q, want the newest", commits[0].Subject)
	}
	if commits[0].Author != "Dan" {
		t.Errorf("author = %q", commits[0].Author)
	}
}

// Both paths must answer a first release the same way. Their disagreement is
// what produced "No changes." for a release that had plenty.
func TestFirstReleaseListsHistoryLocallyToo(t *testing.T) {
	dir := firstReleaseRepo(t)

	previous, commits, err := changelog.Collect(context.Background(), changelog.Source{
		Dir: dir, Tag: "v1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if previous != "" {
		t.Fatalf("previous = %q, want empty for a first release", previous)
	}
	if len(commits) != 2 {
		t.Errorf("got %d commits, want the whole history", len(commits))
	}
}
