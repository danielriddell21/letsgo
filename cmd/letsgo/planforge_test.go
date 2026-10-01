package main

import (
	"strings"
	"testing"

	plandiff "github.com/danielriddell21/letsgo/plan"
)

func TestRunPlanDiffHasNoJSONForm(t *testing.T) {
	err := runPlan([]string{"--diff", "--json"})
	if err == nil || !strings.Contains(err.Error(), "--diff") {
		t.Errorf("err = %v", err)
	}
}

func opsOf(actions []plandiff.Action) []plandiff.Op {
	ops := make([]plandiff.Op, len(actions))
	for i, a := range actions {
		ops[i] = a.Op
	}
	return ops
}
