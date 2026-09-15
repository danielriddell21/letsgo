package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *Manifest {
	return &Manifest{
		Schema:          Schema,
		Project:         "foo",
		Version:         "1.2.3",
		Tag:             "v1.2.3",
		Commit:          "9f2ab1c643104f949d2202ac497969e4ddbb0899",
		SourceDateEpoch: 1710506445,
		Builder:         Builder{Tool: "letsgo 0.1.0", Go: "go1.24.7"},
		Source:          &Source{Archive: "foo_1.2.3_source.tar.gz", SHA256: "abc"},
		Modules:         Modules{GoSumSHA256: "def", Count: 14},
		Gates:           map[string]string{"smoke": "pass", "apidiff": "pass"},
		Artifacts: []Artifact{{
			Name: "foo_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
			Size: 4812345, SHA256: "111", BinarySHA256: "222",
			Build: Build{
				Flags:   []string{"-trimpath", "-buildvcs=false"},
				LDFlags: "-s -w -X main.version=1.2.3",
				Env:     map[string]string{"CGO_ENABLED": "0", "GOOS": "linux"},
			},
		}},
	}
}

// A manifest that varied between runs would be one more difference
// verification had to forgive.
func TestEncodeIsDeterministic(t *testing.T) {
	first, err := sample().Encode()
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		next, err := sample().Encode()
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("encoding varies between runs:\n%s\n---\n%s", first, next)
		}
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

	again, err := got.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Errorf("round trip changed the manifest:\n%s\n---\n%s", data, again)
	}
}

// A consumer that guesses at an unknown schema is worse than one that refuses.
func TestDecodeRejectsUnknownSchema(t *testing.T) {
	if _, err := Decode([]byte(`{"schema": 99}`)); err == nil {
		t.Error("Decode accepted schema 99, want an error")
	}
	if _, err := Decode([]byte(`{}`)); err == nil {
		t.Error("Decode accepted a missing schema, want an error")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Error("Decode accepted invalid JSON, want an error")
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
	if got.Version != "1.2.3" || len(got.Artifacts) != 1 {
		t.Errorf("read back %+v", got)
	}
	if _, ok := got.Artifact("foo_1.2.3_linux_amd64.tar.gz"); !ok {
		t.Error("Artifact lookup failed")
	}
	if _, ok := got.Artifact("nope"); ok {
		t.Error("Artifact found something that is not there")
	}
}

// go.sum lists each module twice, once for the archive and once for its
// go.mod. Counting lines would report double the real dependency count.
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
}

// A module with no dependencies has no go.sum. That is a fact about the
// release, not a failure.
func TestSummariseModulesToleratesAbsence(t *testing.T) {
	mods, err := SummariseModules(filepath.Join(t.TempDir(), "go.sum"))
	if err != nil {
		t.Fatalf("SummariseModules: %v", err)
	}
	if mods.Count != 0 || mods.GoSumSHA256 != "" {
		t.Errorf("got %+v, want zero", mods)
	}
}

func TestSortArtifacts(t *testing.T) {
	artifacts := []Artifact{{Name: "c"}, {Name: "a"}, {Name: "b"}}
	SortArtifacts(artifacts)
	if artifacts[0].Name != "a" || artifacts[2].Name != "c" {
		t.Errorf("SortArtifacts produced %+v", artifacts)
	}
}

// Both spellings describe the same thing, and every caller that keys on a
// binary reads them through here. One that read Binary directly would see an
// archive of eleven tools as an archive of none.
func TestExecutablesReadsBothSpellings(t *testing.T) {
	single := Artifact{Binary: "fish", BinarySize: 42, BinarySHA256: "abc"}
	got := single.Executables()
	if len(got) != 1 || got[0].Name != "fish" || got[0].Size != 42 || got[0].SHA256 != "abc" {
		t.Errorf("Executables() = %+v", got)
	}

	several := Artifact{Binaries: []Binary{{Name: "crabs"}, {Name: "duck"}}}
	if names := several.BinaryNames(); strings.Join(names, ",") != "crabs,duck" {
		t.Errorf("BinaryNames() = %q", names)
	}

	// An archive carrying neither is not an archive of one nameless binary.
	if got := (Artifact{}).Executables(); got != nil {
		t.Errorf("Executables() = %+v, want nil", got)
	}
	if got := (Artifact{}).BinaryNames(); len(got) != 0 {
		t.Errorf("BinaryNames() = %q, want none", got)
	}
}

// The base name is the formula's name and the name a rebuild must reproduce,
// so recovering it wrongly is not a cosmetic error.
func TestBaseNameStripsWhatTheBuilderAppended(t *testing.T) {
	for _, tt := range []struct {
		archive, version, goos, goarch, want string
	}{
		{"toolshed_1.2.3_linux_amd64.tar.gz", "1.2.3", "linux", "amd64", "toolshed"},
		{"toolshed_1.2.3_windows_amd64.zip", "1.2.3", "windows", "amd64", "toolshed"},
		// A variant's suffix is part of the name, not part of the platform.
		{"gambit-gui_1.2.3_darwin_arm64.tar.gz", "1.2.3", "darwin", "arm64", "gambit-gui"},
		// A prerelease version contains a hyphen, which must not be mistaken
		// for a separator.
		{"tool_1.2.3-rc.1_linux_arm64.tar.gz", "1.2.3-rc.1", "linux", "arm64", "tool"},
		// Nothing recoverable: an archive this release did not name.
		{"tool_9.9.9_linux_amd64.tar.gz", "1.2.3", "linux", "amd64", ""},
		{"letsgo.json", "1.2.3", "linux", "amd64", ""},
	} {
		if got := BaseName(tt.archive, tt.version, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("BaseName(%q) = %q, want %q", tt.archive, got, tt.want)
		}
	}

	a := Artifact{Name: "toolshed_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64"}
	if got := a.BaseName("1.2.3"); got != "toolshed" {
		t.Errorf("Artifact.BaseName() = %q", got)
	}
}
