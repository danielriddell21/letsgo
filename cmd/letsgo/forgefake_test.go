package main

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/forgetest"
)

// newFakeForge starts a fake forge for the one repository you/demo and returns
// it with a forge wired to it, and sets the token a real run would find in the
// environment.
func newFakeForge(t *testing.T) (*forgetest.Fake, forge) {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "test-token")

	ff := forgetest.NewFake(t, "you/demo")
	return ff, forge{endpoint: ff.URL()}
}
