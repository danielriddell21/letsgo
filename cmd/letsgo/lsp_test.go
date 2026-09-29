package main

import (
	"strings"
	"testing"
)

func TestRunLSPRejectsExtraArgs(t *testing.T) {
	err := runLSP([]string{"extra"})
	if err == nil || !strings.HasPrefix(err.Error(), "usage: ") {
		t.Errorf("runLSP([extra]) = %v, want a usage error", err)
	}
}
