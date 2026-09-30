package audit

import (
	"reflect"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/manifest"
)

func artifact(tags, goos, goarch string) manifest.Artifact {
	flags := []string{"-trimpath", "-buildvcs=false"}
	if tags != "" {
		flags = append(flags, "-tags="+tags)
	}
	return manifest.Artifact{Build: manifest.Build{
		Flags: flags,
		Env:   map[string]string{"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch},
	}}
}

// The source is scanned as each archive was built: its tags and its target,
// once per distinct combination (AU-4).
func TestScansForCoversEveryRecordedConfiguration(t *testing.T) {
	m := &manifest.Manifest{Artifacts: []manifest.Artifact{
		artifact("", "linux", "amd64"),
		artifact("", "linux", "amd64"),
		artifact("netgo,osusergo", "linux", "amd64"),
		artifact("", "windows", "arm64"),
	}}

	want := []gate.Scan{
		{Env: []string{"CGO_ENABLED=0", "GOARCH=amd64", "GOOS=linux"}},
		{Tags: []string{"netgo", "osusergo"}, Env: []string{"CGO_ENABLED=0", "GOARCH=amd64", "GOOS=linux"}},
		{Env: []string{"CGO_ENABLED=0", "GOARCH=arm64", "GOOS=windows"}},
	}
	if got := scansFor(m); !reflect.DeepEqual(got, want) {
		t.Errorf("scans = %+v, want %+v", got, want)
	}
}

// A manifest with nothing recorded leaves govulncheck's own defaults.
func TestScansForWithoutArtifactsIsEmpty(t *testing.T) {
	if got := scansFor(&manifest.Manifest{}); len(got) != 0 {
		t.Errorf("scans = %+v, want none", got)
	}
}
