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

// A variant ships beside the release it varies, so the manifest holds two
// builds of the same command. They rebuild separately, each with the tags it
// was compiled with — which is the whole reason the flags are recorded.
func TestRebuildGroupsSeparatesAVariantFromTheReleaseItVaries(t *testing.T) {
	tagged := manifest.Build{Flags: []string{"-trimpath", "-buildvcs=false", "-tags=ebiten"}}
	m := &manifest.Manifest{
		Version: "1.0.0",
		Artifacts: []manifest.Artifact{
			{
				Name: "gambit_1.0.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
				Binary: "gambit", BinarySHA256: "a",
				Build: manifest.Build{Flags: []string{"-trimpath", "-buildvcs=false"}},
			},
			{
				Name: "gambit_1.0.0_windows_amd64.zip", OS: "windows", Arch: "amd64",
				Binary: "gambit.exe", BinarySHA256: "b",
				Build: manifest.Build{Flags: []string{"-trimpath", "-buildvcs=false"}},
			},
			{
				Name: "gambit-gui_1.0.0_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64",
				Binary: "gambit-gui", BinarySHA256: "c", Build: tagged,
			},
		},
	}

	commands := []discover.MainPackage{{RelPath: ".", BinaryName: "gambit"}}

	groups, err := rebuildGroups(m, commands)
	if err != nil {
		t.Fatalf("rebuildGroups() error = %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want the release and its variant: %+v", len(groups), groups)
	}

	base, variant := groups[0], groups[1]
	if base.name != "gambit" || len(base.targets) != 2 {
		t.Errorf("base group = %+v", base)
	}
	if variant.name != "gambit-gui" || len(variant.targets) != 1 {
		t.Errorf("variant group = %+v", variant)
	}

	// Without the tags the variant would rebuild as the release did and its
	// digest would not match, which is a verification failure reported against
	// an honest release.
	if got := strings.Join(recordedTags(variant.artifacts), ","); got != "ebiten" {
		t.Errorf("variant tags = %q, want ebiten", got)
	}
	if got := recordedTags(base.artifacts); len(got) != 0 {
		t.Errorf("base tags = %q, want none", got)
	}
}
