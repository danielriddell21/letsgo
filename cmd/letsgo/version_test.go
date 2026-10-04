package main

import (
	"strings"
	"testing"
)

func TestRunVersionJSONListsCapabilities(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runVersion([]string{"--json"}); err != nil {
			t.Fatalf("runVersion: %v", err)
		}
	})
	for _, want := range []string{`"schema": 1`, `"version": "` + version + `"`, `"tag-ref"`, `"lsp"`} {
		if !strings.Contains(out, want) {
			t.Errorf("version --json = %s, want %q", out, want)
		}
	}

	plain := captureStdout(t, func() {
		if err := runVersion(nil); err != nil {
			t.Fatalf("runVersion: %v", err)
		}
	})
	if strings.TrimSpace(plain) != "letsgo "+version {
		t.Errorf("version = %q, want the one-line form", plain)
	}
}
