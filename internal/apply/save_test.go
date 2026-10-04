package apply

import (
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func releasePlan(tag, commit string) *plan.Plan {
	p := &plan.Plan{Tag: tag}
	p.Git.Commit = commit
	return p
}

func TestSaveWritesAReadableFile(t *testing.T) {
	p := releasePlan("v1.3.0", "abc")
	p.Repo.Owner, p.Repo.Name = "you", "demo"
	path := filepath.Join(t.TempDir(), "letsgo.plan")

	digest, err := Save(p, &Diff{
		Actions:        []plandiff.Action{{Op: plandiff.Add, Kind: plandiff.KindAsset, Target: "a.zip", Planned: "sha256:aa"}},
		Manifest:       []byte(`{"version":"1.3.0"}`),
		ManifestSHA256: "sha256:bb",
	}, path, "test")
	if err != nil {
		t.Fatal(err)
	}

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := file.Digest(); got != digest {
		t.Errorf("digest %s, printed %s", got, digest)
	}
	if file.Repo != "you/demo" || file.Tag != "v1.3.0" || file.Commit != "abc" || file.ManifestSHA256 != "sha256:bb" || len(file.Actions) != 1 || file.LetsgoVersion != "test" {
		t.Errorf("saved %+v", file)
	}
}

func TestSaveReportsAnUnwritablePath(t *testing.T) {
	_, err := Save(releasePlan("v1", "abc"), &Diff{Manifest: []byte(`{}`)}, filepath.Join(t.TempDir(), "no", "dir", "p"), "test")
	if err == nil {
		t.Error("Save succeeded into a missing directory")
	}
}

func TestWriteReportsAFileItCannotDigest(t *testing.T) {
	file := &plandiff.File{Schema: plandiff.FileSchema, Manifest: []byte("{bad")}
	if _, err := Write(file, filepath.Join(t.TempDir(), "p")); err == nil {
		t.Error("Write accepted a manifest that is not JSON")
	}
}

func TestSaveYankWritesAReadableYankPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yank.plan")
	actions := []plandiff.Action{{Kind: plandiff.KindGoMod, Target: "go.mod", Op: plandiff.Change}}

	digest, err := SaveYank(YankPlan{Repo: "you/demo", Tag: "v1.2.3", Reason: "broken", Previous: "v1.2.2", Actions: actions}, path, "test")
	if err != nil {
		t.Fatal(err)
	}
	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := file.Digest()
	if err != nil || got != digest {
		t.Fatalf("digest = %q, %v, want %q", got, err, digest)
	}
	if file.Kind != plandiff.FileKindYank || file.Repo != "you/demo" || file.Tag != "v1.2.3" ||
		file.Reason != "broken" || file.Previous != "v1.2.2" || file.LetsgoVersion != "test" || len(file.Actions) != 1 {
		t.Errorf("file = %+v", file)
	}
}
