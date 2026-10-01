package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// tagsRepo makes a one-commit git repository and tags it with tags, for
// tests that need real tag history rather than a fixed list.
func tagsRepo(t *testing.T, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "first")
	for _, tag := range tags {
		run("tag", tag)
	}
	return dir
}

// A prerelease pushes its version and channel unconditionally, and nothing
// floating: PR-6 names no "newer than" check for it.
func TestImageTagsForAPrerelease(t *testing.T) {
	p := &plan.Plan{Version: "1.3.0-rc.1", RootDir: t.TempDir()}

	tags, floating, err := imageTags(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0] != "1.3.0-rc.1" || tags[1] != "rc" {
		t.Errorf("tags = %v, want [1.3.0-rc.1 rc]", tags)
	}
	if floating != nil {
		t.Errorf("floating = %v, want nil", floating)
	}
}

// A snapshot never touches the registry, so it gets no floating tags either.
func TestImageTagsForASnapshot(t *testing.T) {
	p := &plan.Plan{GitBin: "git", Version: "1.3.0", Snapshot: true, RootDir: t.TempDir()}

	tags, floating, err := imageTags(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "1.3.0" {
		t.Errorf("tags = %v, want [1.3.0]", tags)
	}
	if floating != nil {
		t.Errorf("floating = %v, want nil", floating)
	}
}

// A stable release's version tag is unconditional; major.minor, major and
// latest are floating candidates a push only advances if newer (PR-7).
func TestImageTagsForAStableRelease(t *testing.T) {
	p := &plan.Plan{GitBin: "git", Version: "1.3.0", RootDir: tagsRepo(t, "v1.3.0")}

	tags, floating, err := imageTags(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "1.3.0" {
		t.Errorf("tags = %v, want [1.3.0]", tags)
	}

	want := []string{"1.3", "1", "latest"}
	if len(floating) != len(want) {
		t.Fatalf("floating = %v, want %v", floating, want)
	}
	for i, tag := range want {
		if floating[i] != tag {
			t.Errorf("floating[%d] = %q, want %q", i, floating[i], tag)
		}
	}
}

// A stable release's floating tags also carry every channel this module has
// ever published a prerelease under, deduped and sorted, and never a channel
// belonging to a different module's tags.
func TestImageTagsForAStableReleaseCarriesChannelHistory(t *testing.T) {
	dir := tagsRepo(t, "v1.0.0", "v1.1.0-rc.1", "v1.1.0-rc.2", "v1.1.0-beta.1", "other/v1.1.0-alpha.1")
	p := &plan.Plan{GitBin: "git", Version: "1.2.0", RootDir: dir}

	_, floating, err := imageTags(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}

	wantTail := []string{"beta", "rc"}
	if len(floating) != 3+len(wantTail) {
		t.Fatalf("floating = %v, want major.minor/major/latest plus %v", floating, wantTail)
	}
	for i, channel := range wantTail {
		if got := floating[3+i]; got != channel {
			t.Errorf("floating[%d] = %q, want %q", 3+i, got, channel)
		}
	}
}

// A module scoped to a prefix only sees its own prereleases' channels, not
// another module's tags sharing the same repository.
func TestChannelHistoryIgnoresOtherScopes(t *testing.T) {
	dir := tagsRepo(t, "v1.0.0", "services/api/v1.0.0-rc.1")
	p := &plan.Plan{GitBin: "git", RootDir: dir, Scope: discover.Scope{}}

	channels, err := channelHistory(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 0 {
		t.Errorf("channels = %v, want none: the rc tag belongs to services/api", channels)
	}

	scoped := &plan.Plan{GitBin: "git", RootDir: dir, Scope: discover.Scope{Dir: "services/api", Prefix: "services/api/"}}
	channels, err = channelHistory(context.Background(), scoped)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0] != "rc" {
		t.Errorf("channels = %v, want [rc]", channels)
	}
}
