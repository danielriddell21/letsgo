package promote

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/manifest"
)

func baseManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Schema:          manifest.Schema,
		Project:         "foo",
		Version:         "1.3.0-rc.1",
		Tag:             "v1.3.0-rc.1",
		Commit:          "abc123",
		SourceDateEpoch: 1700000000,
		ModuleDir:       "",
		TagPrefix:       "",
		Builder: manifest.Builder{
			Tool: "letsgo",
			Go:   "go1.23.0",
			Plugins: []manifest.BuilderPlugin{
				{Hook: "archive-layout", Command: "letsgo-multi", Version: "1.0.0", Digest: "deadbeef"},
			},
		},
		Modules: manifest.Modules{
			GoSumSHA256: "sum123",
			Count:       2,
			List: []manifest.Module{
				{Path: "example.com/a", Version: "v1.0.0"},
				{Path: "example.com/b", Version: "v2.0.0"},
			},
		},
		Features: &manifest.Features{Disabled: []string{"proxy-warm"}},
		Artifacts: []manifest.Artifact{
			{
				Name: "foo_1.3.0-rc.1_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
				Binary: "foo", SHA256: "sha-rc-linux",
				Build: manifest.Build{Flags: []string{"-trimpath"}, Env: map[string]string{"CGO_ENABLED": "0"}},
			},
			{
				Name: "foo_1.3.0-rc.1_darwin_arm64.tar.gz", OS: "darwin", Arch: "arm64",
				Binary: "foo", SHA256: "sha-rc-darwin",
				Build: manifest.Build{Flags: []string{"-trimpath"}, Env: map[string]string{"CGO_ENABLED": "0"}},
			},
		},
	}
}

// stableFrom clones rc into a plausible stable-release manifest: the fields
// promotion is allowed to change (version, tag, artifact name and digest)
// differ; everything Compare checks is left the same.
func stableFrom(rc *manifest.Manifest) *manifest.Manifest {
	s := *rc
	s.Version = "1.3.0"
	s.Tag = "v1.3.0"
	s.Artifacts = make([]manifest.Artifact, len(rc.Artifacts))
	for i, a := range rc.Artifacts {
		a.Name = strings.Replace(a.Name, "1.3.0-rc.1", "1.3.0", 1)
		a.SHA256 = a.SHA256 + "-stable"
		s.Artifacts[i] = a
	}
	return &s
}

func TestCompare_IdenticalRebuild(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)

	if diffs := Compare(rc, stable); len(diffs) != 0 {
		t.Fatalf("expected no diffs for a legitimate rebuild, got: %v", diffs)
	}
}

func TestCompare_VersionArchiveNameAndDigestAreAllowedToDiffer(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)

	// Sanity: the two really do differ in exactly the ways promotion expects.
	if rc.Version == stable.Version {
		t.Fatal("test fixture is not exercising a version difference")
	}
	if rc.Artifacts[0].Name == stable.Artifacts[0].Name {
		t.Fatal("test fixture is not exercising an archive name difference")
	}
	if rc.Artifacts[0].SHA256 == stable.Artifacts[0].SHA256 {
		t.Fatal("test fixture is not exercising a digest difference")
	}

	if diffs := Compare(rc, stable); len(diffs) != 0 {
		t.Fatalf("version/name/digest differences alone should not be reported, got: %v", diffs)
	}
}

func TestCompare_CommitDiffers(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Commit = "different"

	assertContains(t, Compare(rc, stable), "commit")
}

func TestCompare_GoVersionDiffers(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Builder.Go = "go1.24.0"

	assertContains(t, Compare(rc, stable), "go version")
}

func TestCompare_ModuleListDiffers(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Modules.List = append(append([]manifest.Module{}, rc.Modules.List...),
		manifest.Module{Path: "example.com/c", Version: "v3.0.0"})

	assertContains(t, Compare(rc, stable), "example.com/c")
}

func TestCompare_ModuleVersionDiffers(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Modules.List = append([]manifest.Module{}, rc.Modules.List...)
	stable.Modules.List[0].Version = "v1.0.1"

	assertContains(t, Compare(rc, stable), "example.com/a")
}

func TestCompare_PluginDiffers(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Builder.Plugins = []manifest.BuilderPlugin{
		{Hook: "archive-layout", Command: "letsgo-multi", Version: "2.0.0", Digest: "deadbeef"},
	}

	assertContains(t, Compare(rc, stable), "plugin")
}

func TestCompare_MissingTarget(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Artifacts = stable.Artifacts[:1] // drops darwin/arm64

	diffs := Compare(rc, stable)
	assertContains(t, diffs, "darwin/arm64")
	assertContains(t, diffs, "missing from the rebuild")
}

func TestCompare_ExtraTarget(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	extra := stable.Artifacts[0]
	extra.OS, extra.Arch = "windows", "amd64"
	stable.Artifacts = append(stable.Artifacts, extra)

	diffs := Compare(rc, stable)
	assertContains(t, diffs, "windows/amd64")
	assertContains(t, diffs, "not in")
}

func TestCompare_BuildFlagsDiffer(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Artifacts[0].Build.Flags = []string{"-trimpath", "-race"}

	assertContains(t, Compare(rc, stable), "build flags")
}

func TestCompare_BinaryNamesDiffer(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Artifacts[0].Binary = "bar"

	assertContains(t, Compare(rc, stable), "binaries")
}

func TestCompare_FeaturesDiffer(t *testing.T) {
	rc := baseManifest()
	stable := stableFrom(rc)
	stable.Features = &manifest.Features{Disabled: []string{"changelog"}}

	assertContains(t, Compare(rc, stable), "disabled features")
}

func assertContains(t *testing.T, diffs []string, substr string) {
	t.Helper()
	for _, d := range diffs {
		if strings.Contains(d, substr) {
			return
		}
	}
	t.Fatalf("expected a diff containing %q, got: %v", substr, diffs)
}
