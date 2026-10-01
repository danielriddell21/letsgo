package main

import (
	"strings"
	"testing"
)

func TestRunPlanDiffHasNoJSONForm(t *testing.T) {
	err := unwired.runPlan([]string{"--diff", "--json"})
	if err == nil || !strings.Contains(err.Error(), "--diff") {
		t.Errorf("err = %v", err)
	}
}
