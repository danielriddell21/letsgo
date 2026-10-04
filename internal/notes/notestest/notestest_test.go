package notestest

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/manifest"
)

func TestHistoryHasTwoTags(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "git", "-C", History(t), "tag").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(out)); len(got) != 2 {
		t.Errorf("tags = %v, want two", got)
	}
}

func TestForgeServesTheReleaseAndItsManifest(t *testing.T) {
	m := &manifest.Manifest{Schema: manifest.Schema, Version: "v1.0.0"}
	client := Forge(t, "you/demo", "v1.0.0", m)

	rel, err := client.ReleaseByTag(context.Background(), github.Repo{Owner: "you", Name: "demo"}, "v1.0.0")
	if err != nil || len(rel.Assets) != 1 {
		t.Fatalf("release = %+v, %v, want the manifest asset", rel, err)
	}
	if _, err := client.DownloadAsset(context.Background(), github.Repo{Owner: "you", Name: "demo"}, rel.Assets[0].ID); err != nil {
		t.Errorf("DownloadAsset: %v", err)
	}
}
