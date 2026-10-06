package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// scopedModuleFixture writes a repository with a nested module, versioned
// under its own directory the way a monorepo tags it — the scenario
// docs/hld/monorepo.md exists for.
func scopedModuleFixture(t *testing.T) (repoDir, moduleDir string) {
	t.Helper()
	repoDir = t.TempDir()
	moduleDir = filepath.Join(repoDir, "services", "api")

	for name, content := range map[string]string{
		"go.mod":                  "module github.com/you/foo\n\ngo 1.24\n",
		"services/api/go.mod":     "module github.com/you/foo/services/api\n\ngo 1.24\n",
		"services/api/letsgo.mod": "build " + gobuild.Host().String() + "\n",
	} {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Set locally rather than passed as -c on each command: runTag makes its
	// own git calls against this repository (discover.CreateTag among them),
	// and those need an identity too, not just the ones this fixture runs
	// itself.
	for _, args := range [][]string{
		{"-C", repoDir, "init", "-q", "-b", "main"},
		{"-C", repoDir, "config", "user.name", "Test"},
		{"-C", repoDir, "config", "user.email", "t@example.com"},
		{"-C", repoDir, "add", "."},
		{"-C", repoDir, "commit", "-q", "-m", "first"},
		{"-C", repoDir, "tag", "services/api/v1.2.3"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repoDir, moduleDir
}

// `letsgo tag`, run from a nested module's own directory, proposes and
// creates a tag scoped to it — its own directory as a prefix, not a bare
// version tag that would collide with the repository's own scope.
func TestRunTagCreatesAScopedTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	if err := runTag([]string{"--yes"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	// The fixture's HEAD is already tagged services/api/v1.2.3, so that tag
	// itself is excluded as "the release being made"; with nothing else
	// reachable, this is a first release for the previous tag to find, and
	// bump.Propose's own answer for that is v0.1.0.
	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v0.1.0") {
		t.Errorf("tags at HEAD = %q, want services/api/v0.1.0 among them", out)
	}
}

// --pre proposes a prerelease instead of a stable version: the same base
// bump.Propose computes, with an auto-numbered "-rc.N" suffix.
func TestRunTagCreatesAPrereleaseTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	if err := runTag([]string{"--yes", "--pre"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v0.1.0-rc.1") {
		t.Errorf("tags at HEAD = %q, want services/api/v0.1.0-rc.1", out)
	}
}

// --json --yes creates the tag and reports its full, scope-prefixed ref, so a
// caller never scrapes prose for it; --json alone creates nothing.
func TestRunTagJSONReportsTheRef(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	tagsAtHead := func() string {
		out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
		if err != nil {
			t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
		}
		return string(out)
	}

	dry := captureStdout(t, func() {
		if err := runTag([]string{"--json"}); err != nil {
			t.Fatalf("runTag --json: %v", err)
		}
	})
	if !strings.Contains(dry, `"ref": "services/api/v0.1.0"`) || strings.Contains(dry, `"tagged"`) {
		t.Errorf("dry run = %s, want the ref and no tagged flag", dry)
	}
	if strings.Contains(tagsAtHead(), "v0.1.0") {
		t.Fatal("--json alone created a tag")
	}

	made := captureStdout(t, func() {
		if err := runTag([]string{"--json", "--yes"}); err != nil {
			t.Fatalf("runTag --json --yes: %v", err)
		}
	})
	if !strings.Contains(made, `"ref": "services/api/v0.1.0"`) || !strings.Contains(made, `"tagged": true`) {
		t.Errorf("created = %s, want the ref and tagged: true", made)
	}
	if !strings.Contains(tagsAtHead(), "services/api/v0.1.0") {
		t.Error("--json --yes did not create the tag")
	}
}

// commitAndTag writes a file, commits it, and (if tag is non-empty) tags the
// commit — the shared pattern behind every test that moves HEAD past an
// existing tag before letting runTag propose from a fresh, untagged commit.
func commitAndTag(t *testing.T, repoDir, file, tag string) {
	t.Helper()
	path := filepath.Join(repoDir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repoDir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", full, err, out)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "commit "+file)
	if tag != "" {
		run("tag", tag)
	}
}

// A repeated --pre run against the same base advances rc.1, rc.2, ...
// instead of colliding on the same candidate tag.
func TestRunTagIncrementsAnExistingPrerelease(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)

	// The fixture already tagged HEAD as the stable services/api/v1.2.3; move
	// past it, tag a first prerelease of the next patch, then move past that
	// too so runTag proposes from an untagged HEAD again.
	commitAndTag(t, repoDir, "services/api/a.txt", "services/api/v1.2.4-rc.1")
	commitAndTag(t, repoDir, "services/api/b.txt", "")

	t.Chdir(moduleDir)
	if err := runTag([]string{"--yes", "--patch", "--pre"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v1.2.4-rc.2") {
		t.Errorf("tags at HEAD = %q, want services/api/v1.2.4-rc.2", out)
	}
}

// A proposal must skip an intervening prerelease and bump from the last
// stable: without --pre, "previous" is always "the highest stable release,
// full stop," not whatever tag git describe happens to be nearest to.
func TestRunTagSkipsAnInterveningPrereleaseTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)

	// The fixture already tagged HEAD as the stable services/api/v1.2.3; move
	// past it with a higher prerelease, then an untagged commit for runTag to
	// propose from.
	commitAndTag(t, repoDir, "services/api/a.txt", "services/api/v1.3.0-rc.1")
	commitAndTag(t, repoDir, "services/api/b.txt", "")

	t.Chdir(moduleDir)
	if err := runTag([]string{"--yes", "--patch"}); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "services/api/v1.2.4") {
		t.Errorf("tags at HEAD = %q, want services/api/v1.2.4 (a patch of the last stable, not the rc)", out)
	}
}

// `tag --json` is a dry run: it prints the proposal and creates nothing, so
// an editor can show it without the write `--yes` performs.
func TestRunTagPrintsJSONWhenRequestedAndCreatesNoTag(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	t.Chdir(moduleDir)

	out := captureStdout(t, func() {
		if err := runTag([]string{"--json"}); err != nil {
			t.Fatalf("runTag: %v", err)
		}
	})

	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runTag --json output = %q, want it to contain a schema field", out)
	}

	tags, err := exec.Command("git", "-C", repoDir, "tag", "--points-at", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --points-at HEAD: %v\n%s", err, tags)
	}
	if strings.Contains(string(tags), "v0.1.0") {
		t.Errorf("tags at HEAD = %q, want no new tag from a --json dry run", tags)
	}
}

// A dirty worktree blocks a real tag (a tag names a commit, so uncommitted
// work has to be dealt with first), but --json is read-only and an editor
// needs it to keep working while a file is being edited.
func TestRunTagJSONWorksWithUncommittedChanges(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(moduleDir)

	out := captureStdout(t, func() {
		if err := runTag([]string{"--json"}); err != nil {
			t.Fatalf("runTag: %v", err)
		}
	})

	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runTag --json output = %q, want it to contain a schema field", out)
	}
}
