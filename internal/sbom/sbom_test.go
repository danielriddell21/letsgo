package sbom_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/sbom"
)

func options() sbom.Options {
	return sbom.Options{
		Project:    "foo",
		ModulePath: "github.com/You/Foo",
		Version:    "1.2.3",
		Commit:     "9f2ab1c4",
		Repo:       "you/foo",
		Created:    time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC),
		Tool:       "0.4.0",
		GoVersion:  "go1.27.1",
		Modules: []manifest.Module{
			{Path: "golang.org/x/net", Version: "v0.23.0"},
			{Path: "github.com/pkg/errors", Version: "v0.9.1"},
		},
		Artifacts: []manifest.Artifact{
			{Name: "foo_1.2.3_linux_amd64.tar.gz", SHA256: "aaa"},
		},
	}
}

func generate(t *testing.T, o sbom.Options) map[string]any {
	t.Helper()
	data, err := sbom.Generate(o)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	return doc
}

func TestGenerateProducesValidCycloneDX(t *testing.T) {
	doc := generate(t, options())

	if doc["bomFormat"] != "CycloneDX" || doc["specVersion"] != "1.6" {
		t.Errorf("header = %v / %v", doc["bomFormat"], doc["specVersion"])
	}
	if serial, _ := doc["serialNumber"].(string); !strings.HasPrefix(serial, "urn:uuid:") || len(serial) != 45 {
		t.Errorf("serialNumber = %q", serial)
	}

	meta := doc["metadata"].(map[string]any)
	if meta["timestamp"] != "2024-03-15T12:30:45Z" {
		t.Errorf("timestamp = %v, want the commit time", meta["timestamp"])
	}

	root := meta["component"].(map[string]any)
	// purl lowercases the namespace for golang; the version does not change.
	if root["purl"] != "pkg:golang/github.com/you/foo@v1.2.3" {
		t.Errorf("root purl = %v", root["purl"])
	}
}

// The toolchain contributes the runtime and the standard library. Leaving it
// out is how an SBOM ends up unable to answer a question about a
// standard-library advisory.
func TestGenerateIncludesTheToolchain(t *testing.T) {
	data, err := sbom.Generate(options())
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)
	if !strings.Contains(out, "pkg:golang/golang.org/toolchain@go1.27.1") {
		t.Errorf("the toolchain is not a component:\n%s", out)
	}
	for _, want := range []string{
		"pkg:golang/golang.org/x/net@v0.23.0",
		"pkg:golang/github.com/pkg/errors@v0.9.1",
		`"content": "aaa"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGenerateLinksDependenciesToTheRoot(t *testing.T) {
	doc := generate(t, options())

	deps := doc["dependencies"].([]any)
	if len(deps) != 1 {
		t.Fatalf("dependencies = %v", deps)
	}

	entry := deps[0].(map[string]any)
	if entry["ref"] != "pkg:golang/github.com/you/foo@v1.2.3" {
		t.Errorf("ref = %v", entry["ref"])
	}
	// Two modules and the toolchain; the published files are not dependencies.
	if on := entry["dependsOn"].([]any); len(on) != 3 {
		t.Errorf("dependsOn = %v", on)
	}
}

// Most SBOM generators stamp a wall clock and a random serial into every run,
// so two SBOMs for one commit differ and neither can be checked against the
// other. This one has to be byte-identical.
func TestGenerateIsDeterministic(t *testing.T) {
	first, err := sbom.Generate(options())
	if err != nil {
		t.Fatal(err)
	}

	shuffled := options()
	shuffled.Modules[0], shuffled.Modules[1] = shuffled.Modules[1], shuffled.Modules[0]
	second, err := sbom.Generate(shuffled)
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Errorf("two runs differ:\n%s\n---\n%s", first, second)
	}
}

// A different release must not reuse the same serial number.
func TestSerialFollowsTheRelease(t *testing.T) {
	first := generate(t, options())

	other := options()
	other.Commit = "deadbeef"
	second := generate(t, other)

	if first["serialNumber"] == second["serialNumber"] {
		t.Error("two commits produced the same serial number")
	}
}

func TestGenerateRejectsIncompleteInput(t *testing.T) {
	if _, err := sbom.Generate(sbom.Options{Project: "foo"}); err == nil {
		t.Error("want an error without a module path or version")
	}
}
