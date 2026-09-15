package verify

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
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

// A cgo release names the host it was compiled on, because zig's output
// depends on it. Verifying elsewhere is still worth doing — the pure-Go
// artifacts are held to their digests either way — so a different host is a
// warning about what cannot be checked, never a failure.
func TestCCompilerHostDecidesWhatCGoArtifactsAreHeldTo(t *testing.T) {
	local := gobuild.Host().String()

	for _, tt := range []struct {
		name   string
		cc     *manifest.CCompiler
		want   Status
		agrees bool
	}{
		{name: "pure go", cc: nil, agrees: true},
		{
			name:   "same host",
			cc:     &manifest.CCompiler{Name: "zig", Host: local},
			want:   Pass,
			agrees: true,
		},
		{
			name: "another host",
			cc:   &manifest.CCompiler{Name: "zig", Host: "plan9/mips"},
			want: Warn,
		},
		{
			// Every cgo release letsgo publishes records one; a manifest
			// without it is one letsgo did not write, and guessing would be
			// worse than saying so.
			name: "no host recorded",
			cc:   &manifest.CCompiler{Name: "zig"},
			want: Warn,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{}
			got := sameCCompilerHost(result, &manifest.Manifest{
				Builder: manifest.Builder{CC: tt.cc},
			})

			if got != tt.agrees {
				t.Errorf("sameCCompilerHost = %v, want %v", got, tt.agrees)
			}
			if tt.cc == nil {
				if len(result.Checks) != 0 {
					t.Errorf("a pure-Go release reported %+v", result.Checks)
				}
				return
			}
			if len(result.Checks) != 1 || result.Checks[0].Status != tt.want {
				t.Errorf("checks = %+v, want one %s", result.Checks, tt.want)
			}
		})
	}
}

// The narrowing is bounded: it covers artifacts compiled with cgo, and only
// while the host differs. A pure-Go artifact that does not reproduce is a
// failure on any machine, which is the claim the tool exists for.
func TestOnlyCGoArtifactsAreExcusedByADifferentHost(t *testing.T) {
	cgo := manifest.Artifact{Build: manifest.Build{Env: map[string]string{"CGO_ENABLED": "1"}}}
	pure := manifest.Artifact{Build: manifest.Build{Env: map[string]string{"CGO_ENABLED": "0"}}}

	elsewhere := rebuildInputs{sameHost: false}
	if !elsewhere.hostBound(cgo) {
		t.Error("a cgo artifact built on another host should not be held to its digest")
	}
	if elsewhere.hostBound(pure) {
		t.Error("a pure-Go artifact is reproducible anywhere and must still be held to its digest")
	}

	here := rebuildInputs{sameHost: true}
	if here.hostBound(cgo) {
		t.Error("on the recorded host a cgo artifact must be held to its digest")
	}
}

// Warn does not fail a verification, and Fail does: a mismatch put in the
// wrong pile would either hide a real difference or invent one.
func TestHostBoundDifferencesWarnAndOthersFail(t *testing.T) {
	m := &manifest.Manifest{Artifacts: []manifest.Artifact{{}, {}}}

	warned := &Result{}
	(&comparison{matched: 1, hostBound: []string{"linux/amd64: cgofixture rebuilt as aaa, published bbb"}}).
		report(warned, m)
	if !warned.OK() {
		t.Errorf("a cgo difference on another host failed verification: %+v", warned.Checks)
	}
	if !strings.Contains(warned.Checks[0].Detail, "another host") {
		t.Errorf("the warning does not say why: %q", warned.Checks[0].Detail)
	}

	failed := &Result{}
	(&comparison{matched: 1, problems: []string{"linux/amd64: tool rebuilt as aaa, published bbb"}}).report(failed, m)
	if failed.OK() {
		t.Errorf("a pure-Go difference passed verification: %+v", failed.Checks)
	}
}

// Nothing rebuilt is a failure, but "nothing matched because every artifact is
// host-bound" is the narrowed claim working, not the source failing to build.
func TestNothingMatchingIsNotAFailureWhenEverythingIsHostBound(t *testing.T) {
	m := &manifest.Manifest{Artifacts: []manifest.Artifact{{}}}

	result := &Result{}
	(&comparison{hostBound: []string{"linux/amd64: differs"}}).report(result, m)
	if !result.OK() {
		t.Errorf("checks = %+v, want no failure", result.Checks)
	}

	bare := &Result{}
	(&comparison{}).report(bare, m)
	if bare.OK() {
		t.Error("a rebuild that produced nothing at all should fail")
	}
}
