package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/credential"
)

// `letsgo yank`, run from a nested module's own directory, resolves the
// module's scope before it ever needs a token — so a missing token still
// exercises discover.FindGit/discover.NewScope, and fails for the token
// reason, not because the module or its remote could not be found.
func TestRunYankResolvesModuleScopeBeforeRequiringAToken(t *testing.T) {
	repoDir, moduleDir := scopedModuleFixture(t)
	if out, err := exec.Command("git", "-C", repoDir, "remote", "add", "origin",
		"https://github.com/you/foo.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	t.Chdir(moduleDir)

	for _, name := range credential.EnvVars {
		t.Setenv(name, "")
	}

	err := unwired.runYank([]string{"--yes", "services/api/v1.2.3"})
	if err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("runYank error = %v, want it to complain about a missing token", err)
	}
}
