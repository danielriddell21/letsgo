package main

import (
	"errors"
	"os"
	"path/filepath"
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
		_ = unwired.runPlan([]string{"--json"})
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

// A failing plan still prints JSON (the caller can parse the checks to see
// why), and reports the failure through the exit code rather than a text
// line, the same as a passing one.
func TestRunPlanJSONReturnsErrorOnAFailingPlan(t *testing.T) {
	dir := moduleFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var err error
	out := captureStdout(t, func() {
		err = unwired.runPlan([]string{"--json"})
	})

	if !errors.Is(err, errPlanFailed) {
		t.Errorf("runPlan --json error = %v, want errPlanFailed", err)
	}
	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runPlan --json output on failure = %q, want it to still contain the JSON report", out)
	}
}

func TestRunPlanPrintsTextReportByDefault(t *testing.T) {
	dir := moduleFixture(t)
	t.Chdir(dir)

	out := captureStdout(t, func() {
		_ = unwired.runPlan(nil)
	})

	if strings.Contains(out, `"schema"`) {
		t.Errorf("runPlan with no flags produced JSON:\n%s", out)
	}
	if !strings.Contains(out, "gates") {
		t.Errorf("runPlan output = %q, want the grouped text report", out)
	}
}
