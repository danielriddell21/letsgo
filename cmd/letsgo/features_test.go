package main

import (
	"os"
	"strings"
	"testing"
)

func TestListFeatures(t *testing.T) {
	t.Chdir(t.TempDir())

	var buf strings.Builder
	if err := listFeatures(&buf, false); err != nil {
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

// featureLine writes letsgo.mod as config, runs listFeatures in a fresh
// directory, and returns the one line naming a feature by prefix — the
// shape every test below wants, differing only in the config and the
// feature it checks.
func featureLine(t *testing.T, config, prefix string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("letsgo.mod", []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	if err := listFeatures(&buf, false); err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line starting with %q in:\n%s", prefix, buf.String())
	return ""
}

// A repository's own letsgo.mod changes what the command reports, and says
// so — the whole point of asking rather than reading the catalogue alone.
func TestListFeaturesReadsConfig(t *testing.T) {
	line := featureLine(t, "disable sbom\n", "sbom ")
	if !strings.Contains(line, "off") || !strings.Contains(line, "letsgo.mod") {
		t.Errorf("sbom line = %q, want off and letsgo.mod", line)
	}
}

// require shows up alongside on/off, not merely as a hint about what could be
// written: a maintainer asking "what does this repository actually do"
// should not have to re-read letsgo.mod to notice a require line.
func TestListFeaturesShowsRequired(t *testing.T) {
	line := featureLine(t, "require vulncheck\n", "vulncheck ")
	if !strings.Contains(line, "required") || !strings.Contains(line, "letsgo.mod") {
		t.Errorf("vulncheck line = %q, want required and letsgo.mod", line)
	}
}

// An off-by-default feature (brew, image, budget) is on only when its own
// directive appears, never merely because it was not disabled — Set.On alone
// would say "on" for every one of them with no config at all.
func TestListFeaturesOffByDefaultStayOffWithoutTheirDirective(t *testing.T) {
	for _, name := range []string{"budget", "brew", "image"} {
		line := featureLine(t, "disable sbom\n", name+" ")
		if !strings.Contains(line, "off") {
			t.Errorf("%s line = %q, want off", name, line)
		}
	}
}

// The other half of the same guarantee: writing the directive does turn it
// on.
func TestListFeaturesOffByDefaultTurnOnWithTheirDirective(t *testing.T) {
	line := featureLine(t, "brew you/tap\n", "brew ")
	if !strings.Contains(line, "on") || !strings.Contains(line, "letsgo.mod") {
		t.Errorf("brew line = %q, want on and letsgo.mod", line)
	}
}

// A config that fails to parse is reported rather than silently ignored.
func TestListFeaturesRejectsBadConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("letsgo.mod", []byte("disable not-a-feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := listFeatures(&strings.Builder{}, false); err == nil {
		t.Error("listFeatures should have reported the config error")
	}
}

// letsgo features --json prints the same catalogue as the text table, in its
// own schema-versioned wire form.
func TestListFeaturesJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("letsgo.mod", []byte("disable sbom\nrequire vulncheck\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	if err := listFeatures(&buf, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		`"schema": 1`,
		`"name": "sbom"`,
		`"kind": "output"`,
		`"on": false`,
		`"name": "vulncheck"`,
		`"required": true`,
		`"from": "letsgo.mod"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("listFeatures JSON output = %q, want it to contain %q", out, want)
		}
	}
}

func TestRunFeaturesPrintsJSONWhenRequested(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	out := captureStdout(t, func() {
		_ = runFeatures([]string{"--json"})
	})

	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("runFeatures --json output = %q, want it to contain a schema field", out)
	}
}
