package bump

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/git"
)

// scopedModule writes a repository with a nested module, versioned under its
// own directory the way a monorepo tags it, and returns the module's directory.
func scopedModule(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()

	for name, content := range map[string]string{
		"go.mod":              "module github.com/you/foo\n\ngo 1.24\n",
		"services/api/go.mod": "module github.com/you/foo/services/api\n\ngo 1.24\n",
	} {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	return filepath.Join(repoDir, "services", "api")
}

// Propose parses the previous tag as a plain "vX.Y.Z"; a module scoped
// under services/api carries that version behind a "services/api/" prefix,
// which has to come off before ProposeFor hands it to Propose, or
// every scoped tag proposal fails outright.
func TestProposeVersionStripsThePrefixBeforeParsingSemver(t *testing.T) {
	moduleDir := scopedModule(t)
	module := discover.Module{Path: "github.com/you/foo/services/api", Dir: moduleDir}
	scope := discover.Scope{Dir: "services/api", Prefix: "services/api/"}

	proposal, err := Repo{Runner: git.New("git", moduleDir), Module: module, Scope: scope}.ProposeFor(context.Background(), "services/api/v1.2.3", None)
	if err != nil {
		t.Fatalf("ProposeFor: %v", err)
	}
	if proposal.Next != "v1.2.4" {
		t.Errorf("Next = %q, want v1.2.4 (a patch bump of the stripped version)", proposal.Next)
	}
}

// A commit that only touched a further-nested module (one inside the module
// being tagged) is not evidence for this module's own bump: it belongs to a
// release with its own history.
func TestProposeVersionExcludesANestedModulesOwnCommits(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "t@example.com")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("tag", "v1.0.0")

	write("plugin/go.mod", "module github.com/you/foo/plugin\n")
	run("add", ".")
	run("commit", "-q", "-m", "feat!: nested module's own breaking change")

	module := discover.Module{Path: "github.com/you/foo", Dir: dir}
	proposal, err := Repo{Runner: git.New("git", dir), Module: module}.ProposeFor(context.Background(), "v1.0.0", None)
	if err != nil {
		t.Fatalf("ProposeFor: %v", err)
	}
	if proposal.Next != "v1.0.1" {
		t.Errorf("Next = %q, want v1.0.1: the nested module's commit must not count as a signal here", proposal.Next)
	}
}

func TestForcedPicksTheLevelAFlagDemands(t *testing.T) {
	for _, tt := range []struct {
		major, minor, patch bool
		want                Level
	}{
		{false, false, false, None},
		{true, false, false, Major},
		{false, true, false, Minor},
		{false, false, true, Patch},
		{true, true, true, Major},
		{false, true, true, Minor},
	} {
		if got := Forced(tt.major, tt.minor, tt.patch); got != tt.want {
			t.Errorf("Forced(%v, %v, %v) = %v, want %v", tt.major, tt.minor, tt.patch, got, tt.want)
		}
	}
}

func TestNextPrerelease(t *testing.T) {
	tests := []struct {
		name   string
		tags   []string
		prefix string
		base   string
		want   string
	}{
		{"no earlier candidates", nil, "", "v1.3.0", "v1.3.0-rc.1"},
		{"advances past the highest", []string{"v1.3.0-rc.1", "v1.3.0-rc.2"}, "", "v1.3.0", "v1.3.0-rc.3"},
		{"order does not matter", []string{"v1.3.0-rc.10", "v1.3.0-rc.2"}, "", "v1.3.0", "v1.3.0-rc.11"},
		{"another base does not count", []string{"v1.2.0-rc.5"}, "", "v1.3.0", "v1.3.0-rc.1"},
		{"a stable tag does not count", []string{"v1.3.0"}, "", "v1.3.0", "v1.3.0-rc.1"},
		{"a non-numeric suffix is ignored", []string{"v1.3.0-rc.x"}, "", "v1.3.0", "v1.3.0-rc.1"},
		{"scoped tags carry their prefix", []string{"services/api/v1.3.0-rc.4", "v1.3.0-rc.9"}, "services/api/", "v1.3.0", "v1.3.0-rc.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextPrerelease(tt.tags, tt.prefix, tt.base); got != tt.want {
				t.Errorf("NextPrerelease = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProposeForAForcedLevelIgnoresTheSignals(t *testing.T) {
	moduleDir := scopedModule(t)
	module := discover.Module{Path: "github.com/you/foo/services/api", Dir: moduleDir}
	scope := discover.Scope{Dir: "services/api", Prefix: "services/api/"}

	proposal, err := Repo{Runner: git.New("git", moduleDir), Module: module, Scope: scope}.ProposeFor(context.Background(), "services/api/v1.2.3", Minor)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Next != "v1.3.0" || len(proposal.Signals) != 1 || proposal.Signals[0].Source != "you" {
		t.Errorf("proposal = %+v, want v1.3.0 from the one forced signal", proposal)
	}
}
