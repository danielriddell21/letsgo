package main

import (
	"strings"
	"testing"
)

// letsgo plan --json prints the plan in its own schema-versioned wire form
// instead of the human-readable report, and does not print the trailing
// "plan ok"/"plan failed" summary line a machine reader has no use for.
func TestRunPlanPrintsJSONWhenRequested(t *testing.T) {
	dir := moduleFixture(t)
	t.Chdir(dir)

	out := captureStdout(t, func() {
		_ = runPlan([]string{"--json"})
	})

	for _, want := range []string{`"schema": 1`, `"project"`, `"checks"`} {
		if !strings.Contains(out, want) {
			t.Errorf("runPlan --json output = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "plan ok in") {
		t.Errorf("runPlan --json output carries the text summary line:\n%s", out)
	}
}

func TestRunPlanPrintsTextReportByDefault(t *testing.T) {
	dir := moduleFixture(t)
	t.Chdir(dir)

	out := captureStdout(t, func() {
		_ = runPlan(nil)
	})

	if strings.Contains(out, `"schema"`) {
		t.Errorf("runPlan with no flags produced JSON:\n%s", out)
	}
	if !strings.Contains(out, "gates") {
		t.Errorf("runPlan output = %q, want the grouped text report", out)
	}
}
