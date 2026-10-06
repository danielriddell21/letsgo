package main

import (
	"context"
	"testing"
)

// scopePrefix resolves the module's own scope prefix, so verify's "no tag
// given" can stay inside it instead of picking another module's release.
func TestScopePrefixResolvesTheModulesOwnPrefix(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)

	prefix, err := scopePrefix(context.Background(), "git", "", moduleDir)
	if err != nil {
		t.Fatalf("scopePrefix: %v", err)
	}
	if prefix != "services/api/" {
		t.Errorf("prefix = %q, want services/api/", prefix)
	}
}

// A repository named explicitly by --repo has no local module to scope by:
// inspecting a release elsewhere always means the whole repository.
func TestScopePrefixIsEmptyWhenRepoIsExplicit(t *testing.T) {
	_, moduleDir := scopedModuleFixture(t)

	prefix, err := scopePrefix(context.Background(), "git", "you/elsewhere", moduleDir)
	if err != nil {
		t.Fatalf("scopePrefix: %v", err)
	}
	if prefix != "" {
		t.Errorf("prefix = %q, want empty for an explicit --repo", prefix)
	}
}

// A directory outside any git repository has no scope to resolve, and that
// has to surface as an error rather than a silently unscoped lookup.
func TestScopePrefixFailsOutsideAGitRepository(t *testing.T) {
	dir := t.TempDir()

	if _, err := scopePrefix(context.Background(), "git", "", dir); err == nil {
		t.Error("scopePrefix succeeded outside a git repository")
	}
}
