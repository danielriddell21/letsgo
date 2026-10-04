package main

import (
	"strings"
	"testing"
)

// letsgo doctor --json prints the same checks as the text report, in
// doctor's own schema-versioned wire form, rather than the human-readable
// grouped listing.
func TestRunDoctorPrintsJSONWhenRequested(t *testing.T) {
	dir := moduleFixture(t)
	t.Chdir(dir)

	out := captureStdout(t, func() {
		_ = unwired.runDoctor([]string{"--json"})
	})

	for _, want := range []string{`"schema": 1`, `"checks"`, `"group"`, `"status"`} {
		if !strings.Contains(out, want) {
			t.Errorf("runDoctor --json output = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "  tools\n") {
		t.Errorf("runDoctor --json output looks like the text report, not JSON:\n%s", out)
	}
}

// With no flag, the default text report is unchanged: JSON is opt-in.
func TestRunDoctorPrintsTextReportByDefault(t *testing.T) {
	dir := moduleFixture(t)
	t.Chdir(dir)

	out := captureStdout(t, func() {
		_ = unwired.runDoctor(nil)
	})

	if strings.Contains(out, `"schema"`) {
		t.Errorf("runDoctor with no flags produced JSON:\n%s", out)
	}
	if !strings.Contains(out, "tools") {
		t.Errorf("runDoctor output = %q, want the grouped text report", out)
	}
}
