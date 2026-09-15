package verify

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
)

// The manifest is the authority on layout, not the source tree. A release laid
// out by a plugin has to be verifiable on a machine that does not have it —
// otherwise the plugin is an unrecorded build input, which is exactly what the
// contract exists to prevent.
func TestRebuildGroupsReconstructsAPluginLayoutWithoutThePlugin(t *testing.T) {
	m := &manifest.Manifest{
		Version: "0.0.3",
		Artifacts: []manifest.Artifact{
			{
				Name: "toolshed_0.0.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
				Binaries: []manifest.Binary{
					{Name: "crabs", SHA256: "a"},
					{Name: "duck", SHA256: "b"},
					{Name: "fish", SHA256: "c"},
				},
			},
			{
				Name: "toolshed_0.0.3_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64",
				Binaries: []manifest.Binary{
					{Name: "crabs", SHA256: "d"},
					{Name: "duck", SHA256: "e"},
					{Name: "fish", SHA256: "f"},
				},
			},
		},
	}

	commands := []discover.MainPackage{
		{RelPath: "./cmd/crabs", BinaryName: "crabs"},
		{RelPath: "./cmd/duck", BinaryName: "duck"},
		{RelPath: "./cmd/fish", BinaryName: "fish"},
	}

	groups, err := rebuildGroups(m, commands)
	if err != nil {
		t.Fatal(err)
	}

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want the one archive the release published", len(groups))
	}
	if groups[0].name != "toolshed" {
		t.Errorf("name = %q", groups[0].name)
	}
	if len(groups[0].targets) != 2 {
		t.Errorf("targets = %+v, want both platforms", groups[0].targets)
	}

	var packages []string
	for _, c := range groups[0].commands {
		packages = append(packages, c.Package+"="+c.Binary)
	}
	want := "./cmd/crabs=crabs,./cmd/duck=duck,./cmd/fish=fish"
	if strings.Join(packages, ",") != want {
		t.Errorf("commands = %q, want %q", packages, want)
	}
}

// The long-standing single-binary spelling still reconstructs, so releases
// published before any of this keep verifying.
func TestRebuildGroupsReadsTheSingleBinarySpelling(t *testing.T) {
	m := &manifest.Manifest{
		Version: "1.2.3",
		Artifacts: []manifest.Artifact{{
			Name: "foo_1.2.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
			Binary: "foo", BinarySHA256: "a",
		}},
	}

	groups, err := rebuildGroups(m, []discover.MainPackage{{RelPath: ".", BinaryName: "foo"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].name != "foo" {
		t.Fatalf("groups = %+v", groups)
	}
	if len(groups[0].commands) != 1 || groups[0].commands[0].Binary != "foo" {
		t.Errorf("commands = %+v", groups[0].commands)
	}
}

// A published binary this source cannot build means the release and the
// checkout have diverged, which is a verification failure rather than a
// rebuild to attempt anyway.
func TestRebuildGroupsRefusesABinaryTheSourceDoesNotBuild(t *testing.T) {
	m := &manifest.Manifest{
		Version: "0.0.3",
		Artifacts: []manifest.Artifact{{
			Name: "toolshed_0.0.3_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
			Binaries: []manifest.Binary{{Name: "crabs"}, {Name: "gone"}},
		}},
	}

	_, err := rebuildGroups(m, []discover.MainPackage{
		{RelPath: "./cmd/crabs", BinaryName: "crabs"},
		{RelPath: "./cmd/duck", BinaryName: "duck"},
	})
	if err == nil || !strings.Contains(err.Error(), "no main package") {
		t.Errorf("err = %v", err)
	}
}
