package main

import (
	"os"
	"strings"
	"testing"
)

func TestListFeatures(t *testing.T) {
	t.Chdir(t.TempDir())

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
		"default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

// A repository's own letsgo.mod changes what the command reports, and says
// so — the whole point of asking rather than reading the catalogue alone.
func TestListFeaturesReadsConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("letsgo.mod", []byte("disable sbom\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	if err := listFeatures(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "sbom ") {
			if !strings.Contains(line, "off") || !strings.Contains(line, "letsgo.mod") {
				t.Errorf("sbom line = %q, want off and letsgo.mod", line)
			}
		}
	}
}

// A config that fails to parse is reported rather than silently ignored.
func TestListFeaturesRejectsBadConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("letsgo.mod", []byte("disable not-a-feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := listFeatures(&strings.Builder{}); err == nil {
		t.Error("listFeatures should have reported the config error")
	}
}
