package main

import (
	"context"
	"testing"
)

// resolveTarget resolves the module's own scope prefix, so verify's "no tag
// given" can stay inside it instead of picking another module's release.
func TestResolveTargetFindsTheModulesOwnRepositoryAndPrefix(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)

	got, err := resolveTarget(context.Background(), "git", "", moduleDir)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.Prefix != "services/api/" || got.Dir != moduleDir || got.Repo.Owner != "you" || got.Repo.Name != "foo" {
		t.Errorf("target = %+v, want you/foo's services/api module", got)
	}
}

// A repository named explicitly by --repo has no local module to scope by:
// inspecting a release elsewhere always means the whole repository.
func TestResolveTargetIsUnscopedWhenRepoIsExplicit(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)

	got, err := resolveTarget(context.Background(), "git", "you/elsewhere", moduleDir)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.Prefix != "" || got.Dir != "" || got.Repo.Name != "elsewhere" {
		t.Errorf("target = %+v, want the named repository with no scope and no directory", got)
	}
}

func TestResolveTargetRejectsAMalformedRepo(t *testing.T) {
	for _, bad := range []string{"noslash", "/name", "owner/"} {
		if _, err := resolveTarget(context.Background(), "git", bad, "."); err == nil {
			t.Errorf("--repo %q was accepted", bad)
		}
	}
}

// A directory outside any git repository has no scope to resolve, and that
// has to surface as an error rather than a silently unscoped lookup.
func TestResolveTargetFailsOutsideAGitRepository(t *testing.T) {
	if _, err := resolveTarget(context.Background(), "git", "", t.TempDir()); err == nil {
		t.Error("resolveTarget succeeded outside a git repository")
	}
}
