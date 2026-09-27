package main

import (
	"strings"
	"testing"
)

func TestListFeatures(t *testing.T) {
	var buf strings.Builder
	if err := listFeatures(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"reproducible", "vulncheck", "sbom", "brew",
		"cannot be changed",
		"disable, require",
		"enable with `brew`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}
