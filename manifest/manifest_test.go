package manifest

import (
	"os"
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

func TestDecodeStrictRejectsAnUnfamiliarSchema(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{name: "the current schema", data: `{"schema": 1}`},
		{name: "a newer schema", data: `{"schema": 99}`, wantErr: true},
		{name: "a missing schema", data: `{}`, wantErr: true},
		{name: "invalid JSON", data: `not json`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeStrict([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeStrict(%s) error = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestReadStrict(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.json"), filepath.Join(dir, "bad.json")
	if err := sample().Write(good); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`{"schema": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStrict(good); err != nil {
		t.Errorf("ReadStrict(current schema): %v", err)
	}
	if _, err := ReadStrict(bad); err == nil {
		t.Error("ReadStrict accepted schema 99")
	}
	if _, err := ReadStrict(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("ReadStrict should have reported the missing file")
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

func TestReadMissingFile(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("Read should have reported the missing file")
	}
}

func TestWriteToAnUnwritableDirectory(t *testing.T) {
	if err := sample().Write(filepath.Join(t.TempDir(), "nope", FileName)); err == nil {
		t.Error("Write should have reported the missing directory")
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

// Artifact.BaseName is the method form, which every caller outside this
// package actually uses.
func TestArtifactBaseNameMethod(t *testing.T) {
	a := Artifact{Name: "foo_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64"}
	if got := a.BaseName("1.2.3"); got != "foo" {
		t.Errorf("BaseName = %q", got)
	}
}

func TestBinaryNames(t *testing.T) {
	multi := Artifact{Binaries: []Binary{{Name: "a"}, {Name: "b"}}}
	if got := multi.BinaryNames(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("BinaryNames() = %v", got)
	}
}

func TestSortArtifacts(t *testing.T) {
	artifacts := []Artifact{{Name: "b"}, {Name: "a"}}
	SortArtifacts(artifacts)
	if artifacts[0].Name != "a" || artifacts[1].Name != "b" {
		t.Errorf("SortArtifacts did not sort: %+v", artifacts)
	}
}

func TestSummariseModulesCountsDistinctModules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.sum")
	content := strings.Join([]string{
		"github.com/a/b v1.0.0 h1:aaa=",
		"github.com/a/b v1.0.0/go.mod h1:bbb=",
		"golang.org/x/net v0.23.0 h1:ccc=",
		"golang.org/x/net v0.23.0/go.mod h1:ddd=",
		"golang.org/x/sys v0.1.0/go.mod h1:eee=",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mods, err := SummariseModules(path)
	if err != nil {
		t.Fatal(err)
	}
	if mods.Count != 3 {
		t.Errorf("Count = %d, want 3", mods.Count)
	}
	if len(mods.GoSumSHA256) != 64 {
		t.Errorf("GoSumSHA256 = %q, want a sha256", mods.GoSumSHA256)
	}
	if len(mods.List) != 3 || mods.List[0].Path != "github.com/a/b" {
		t.Errorf("List = %+v", mods.List)
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
