package main

import (
	"testing"
)

// A release the forge does not have is a failed verification, reported as an
// error rather than a hang or a panic: the run gets as far as the forge.
func TestRunVerifyReportsAMissingRelease(t *testing.T) {
	t.Chdir(moduleFixtureWith(t, "", nil))

	f := emptyForge(t)
	if err := f.runVerify([]string{"-repo", "you/foo", "-no-rebuild", "-work", t.TempDir(), "v9.9.9"}); err == nil {
		t.Error("verifying a release that does not exist succeeded")
	}
}
