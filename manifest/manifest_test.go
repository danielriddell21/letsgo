package manifest

import (
	"path/filepath"
	"strings"
	"testing"
)

func sample() *Manifest {
	return &Manifest{
		Schema:  Schema,
		Project: "foo",
		Version: "1.2.3",
		Tag:     "v1.2.3",
		Commit:  "9f2ab1c643104f949d2202ac497969e4ddbb0899",
		Builder: Builder{Tool: "letsgo 0.1.0", Go: "go1.24.7"},
		Source:  &Source{Archive: "foo_1.2.3_source.tar.gz", SHA256: "abc"},
		Artifacts: []Artifact{{
			Name: "foo_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
			SHA256: "111", BinarySHA256: "222",
		}},
	}
}

func TestRoundTrip(t *testing.T) {
	data, err := sample().Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Project != "foo" || got.Version != "1.2.3" {
		t.Errorf("got = %+v", got)
	}
}

// The whole point of this package's Decode: a plugin reading a manifest from
// a letsgo of a different schema gets the fields it recognises, not a
// refusal over a number it has no stake in.
func TestDecodeDoesNotRejectAnUnfamiliarSchema(t *testing.T) {
	for _, schema := range []int{0, 1, 2, 99} {
		m := sample()
		m.Schema = schema

		data, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(data)
		if err != nil {
			t.Errorf("schema %d: Decode: %v", schema, err)
			continue
		}
		if got.Schema != schema {
			t.Errorf("schema %d: got.Schema = %d", schema, got.Schema)
		}
	}
}

func TestDecodeRejectsInvalidJSON(t *testing.T) {
	if _, err := Decode([]byte("not json")); err == nil {
		t.Error("Decode accepted invalid JSON")
	}
}

func TestWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := sample().Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "foo" {
		t.Errorf("got = %+v", got)
	}
}

func TestArtifactLookup(t *testing.T) {
	m := sample()
	if _, ok := m.Artifact("foo_1.2.3_linux_amd64.tar.gz"); !ok {
		t.Error("Artifact did not find the published archive")
	}
	if _, ok := m.Artifact("nope"); ok {
		t.Error("Artifact found something that was not published")
	}
}

func TestExecutablesReadsBothSpellings(t *testing.T) {
	single := Artifact{Binary: "foo", BinarySize: 10, BinarySHA256: "abc"}
	if got := single.Executables(); len(got) != 1 || got[0].Name != "foo" {
		t.Errorf("Executables() = %+v", got)
	}

	multi := Artifact{Binaries: []Binary{{Name: "a"}, {Name: "b"}}}
	if got := multi.Executables(); len(got) != 2 {
		t.Errorf("Executables() = %+v", got)
	}
}

func TestBaseNameStripsWhatTheBuilderAppended(t *testing.T) {
	if got := BaseName("foo_1.2.3_linux_amd64.tar.gz", "1.2.3", "linux", "amd64"); got != "foo" {
		t.Errorf("BaseName = %q", got)
	}
	if got := BaseName("foo_1.2.3_windows_amd64.zip", "1.2.3", "windows", "amd64"); got != "foo" {
		t.Errorf("BaseName = %q", got)
	}
}

func TestSortArtifacts(t *testing.T) {
	artifacts := []Artifact{{Name: "b"}, {Name: "a"}}
	SortArtifacts(artifacts)
	if artifacts[0].Name != "a" || artifacts[1].Name != "b" {
		t.Errorf("SortArtifacts did not sort: %+v", artifacts)
	}
}

func TestSummariseModulesToleratesAbsence(t *testing.T) {
	mods, err := SummariseModules(filepath.Join(t.TempDir(), "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if mods.Count != 0 {
		t.Errorf("Count = %d, want 0", mods.Count)
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	first, err := sample().Encode()
	if err != nil {
		t.Fatal(err)
	}
	second, err := sample().Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("Encode produced different output for the same manifest")
	}
	if strings.Contains(string(first), "\\u003c") {
		t.Error("Encode escaped HTML characters")
	}
}
