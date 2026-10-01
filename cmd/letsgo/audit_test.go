package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/audit"
)

func TestReportAuditResultSaysWhatHappened(t *testing.T) {
	for name, tc := range map[string]struct {
		result *audit.Result
		want   string
	}{
		"skipped":   {&audit.Result{Tag: "v1.0.0", Skipped: "the release is immutable"}, "v1.0.0: skipped, the release is immutable"},
		"recorded":  {&audit.Result{Tag: "v1.0.0", Recorded: true}, "recorded in audit.json"},
		"unchanged": {&audit.Result{Tag: "v1.0.0"}, "no change since the last audit"},
	} {
		out := captureStdout(t, func() { reportAuditResult(tc.result) })
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: output = %q, want %q", name, out, tc.want)
		}
	}
}

// A release the forge does not have is an error from the single-tag path.
func TestRunAuditReportsAMissingRelease(t *testing.T) {
	t.Chdir(moduleFixtureWith(t, "", nil))

	f := emptyForge(t)
	if err := f.runAudit([]string{"-repo", "you/foo", "-work", t.TempDir(), "v9.9.9"}); err == nil {
		t.Error("auditing a release that does not exist succeeded")
	}
}

// With no tag, audit sweeps the repository's releases, so a forge that cannot
// list them is an error.
func TestRunAuditWithoutATagListsTheRepositoryReleases(t *testing.T) {
	t.Chdir(moduleFixtureWith(t, "", nil))

	f := emptyForge(t)
	if err := f.runAudit([]string{"-repo", "you/foo", "-work", t.TempDir()}); err == nil {
		t.Error("sweeping a repository whose releases cannot be listed succeeded")
	}
}
