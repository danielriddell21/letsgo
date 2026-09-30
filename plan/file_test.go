package plan_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/plan"
)

func sampleFile() *plan.File {
	return &plan.File{
		Schema: plan.FileSchema, LetsgoVersion: "v0.9.0", CreatedAt: "2026-09-27T10:00:00Z",
		Kind: plan.FileKindRelease, Repo: "you/gambit", Tag: "v1.3.0", Commit: "4f2a9c1",
		ManifestSHA256: digestA, Manifest: json.RawMessage(`{"schema":1,"version":"1.3.0"}`),
		Actions: []plan.Action{{Op: plan.Add, Kind: plan.KindAsset, Target: "a.tar.gz", Planned: digestB}},
	}
}

func TestFileRoundTripsThroughDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "letsgo.plan")
	want := sampleFile()
	if err := want.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := plan.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != want.Tag || got.Commit != want.Commit || got.ManifestSHA256 != want.ManifestSHA256 || len(got.Actions) != 1 {
		t.Errorf("Read = %+v, want %+v", got, want)
	}
	a, _ := want.Digest()
	b, _ := got.Digest()
	if a != b || !strings.HasPrefix(a, "sha256:") {
		t.Errorf("digest changed across a round trip: %s vs %s", a, b)
	}
}

func TestDigestIgnoresHowTheFileIsIndented(t *testing.T) {
	f := sampleFile()
	indented, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, indented); err != nil {
		t.Fatal(err)
	}

	a, err := plan.Decode(indented)
	if err != nil {
		t.Fatal(err)
	}
	b, err := plan.Decode(compact.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	da, _ := a.Digest()
	db, _ := b.Digest()
	if da != db {
		t.Errorf("digest depends on layout: %s vs %s", da, db)
	}
}

func TestDigestChangesWithTheActions(t *testing.T) {
	a := sampleFile()
	b := sampleFile()
	b.Actions[0].Planned = digestA
	da, _ := a.Digest()
	db, _ := b.Digest()
	if da == db {
		t.Error("two different plans share a digest")
	}
}

func TestDecodeRefusesWhatItCannotApply(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*plan.File)
		want   string
	}{
		"schema":   {func(f *plan.File) { f.Schema = 2 }, "schema 2"},
		"kind":     {func(f *plan.File) { f.Kind = "yank" }, `"yank"`},
		"tag":      {func(f *plan.File) { f.Tag = "" }, "missing"},
		"commit":   {func(f *plan.File) { f.Commit = "" }, "missing"},
		"manifest": {func(f *plan.File) { f.ManifestSHA256 = "" }, "missing"},
	} {
		t.Run(name, func(t *testing.T) {
			f := sampleFile()
			tc.mutate(f)
			data, err := f.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Decode(data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Decode = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	if _, err := plan.Decode([]byte("not json")); err == nil {
		t.Error("Decode accepted garbage")
	}
}

func TestReadReportsAMissingFile(t *testing.T) {
	if _, err := plan.Read(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("Read succeeded on a missing file")
	}
}

func TestWriteReportsAnUnwritablePath(t *testing.T) {
	if err := sampleFile().Write(filepath.Join(t.TempDir(), "no", "such", "dir")); err == nil {
		t.Error("Write succeeded into a missing directory")
	}
}

func TestHasChangesIgnoresWhatIsKept(t *testing.T) {
	if plan.HasChanges([]plan.Action{{Op: plan.Keep}, {Op: plan.Keep}}) {
		t.Error("kept actions count as changes")
	}
	if plan.HasChanges(nil) {
		t.Error("an empty plan has changes")
	}
	f := sampleFile()
	if !f.Changes() {
		t.Error("a plan that adds an asset reports no changes")
	}
	f.Actions[0].Op = plan.Keep
	if f.Changes() {
		t.Error("a plan that keeps everything reports changes")
	}
}
