package plugin

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/manifest"
)

// Yank rebuilds the same input a release built, but from the previous
// release's manifest rather than fresh artifacts.
func TestTapFilesInputFromManifestRebuildsFromThePreviousRelease(t *testing.T) {
	m := &manifest.Manifest{
		Project: "gambit", Version: "1.2.0", Tag: "v1.2.0",
		Artifacts: []manifest.Artifact{
			{
				Name: "gambit-gui_1.2.0_darwin_arm64.tar.gz", Variant: "gui",
				OS: "darwin", Arch: "arm64", SHA256: strings.Repeat("a", 64), Binary: "gambit-gui",
			},
		},
	}

	in := TapFilesInputFromManifest(m, "you/gambit", "you/homebrew-tap", "a caveat")

	if in.Project != "gambit" || in.Version != "1.2.0" || in.Tag != "v1.2.0" ||
		in.Repo != "you/gambit" || in.Tap != "you/homebrew-tap" || in.Caveats != "a caveat" ||
		in.Homepage != "https://you/gambit" {
		t.Errorf("in = %+v", in)
	}
	if len(in.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", in.Artifacts)
	}

	a := in.Artifacts[0]
	if a.Archive != m.Artifacts[0].Name || a.Variant != "gui" || a.SHA256 != strings.Repeat("a", 64) ||
		a.URL != "https://github.com/you/gambit/releases/download/v1.2.0/"+m.Artifacts[0].Name ||
		len(a.Binaries) != 1 || a.Binaries[0] != "gambit-gui" {
		t.Errorf("artifact = %+v", a)
	}
}

// A release published before the manifest recorded a tag has none to read,
// so the same v-prefixed default a fresh release would use applies here too.
func TestTapFilesInputFromManifestDefaultsTheTagFromTheVersion(t *testing.T) {
	m := &manifest.Manifest{Project: "gambit", Version: "1.2.0"}

	in := TapFilesInputFromManifest(m, "you/gambit", "", "")
	if in.Tag != "v1.2.0" {
		t.Errorf("Tag = %q, want v1.2.0", in.Tag)
	}
}

func TestDownloadURLKeepsAScopedTagsSlashAndEscapesTheName(t *testing.T) {
	got := DownloadURL("you/mono", "tools/cli/v1.0.0", "a b.tar.gz")
	want := "https://github.com/you/mono/releases/download/tools/cli/v1.0.0/a%20b.tar.gz"
	if got != want {
		t.Errorf("DownloadURL = %q, want %q", got, want)
	}
}
