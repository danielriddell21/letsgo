package publication

import (
	"fmt"
	"testing"

	"github.com/danielriddell21/letsgo/internal/release"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/plan"
)

func indexBody(version string) string {
	return fmt.Sprintf(`{"annotations":{"org.opencontainers.image.version":%q}}`, version)
}

func TestObserveTag(t *testing.T) {
	releasing, _ := semver.Parse("1.2.0")
	current := map[string]string{"1.2.0": "1.2.0", "1": "1.1.0", "2": "1.3.0"}
	f := newFakeManifests(t, current)
	built := release.ImageBuild{Registry: "ghcr.io", Repository: "you/tool"}
	same := string(oci.DigestOf([]byte(indexBody("1.2.0"))))

	tests := []struct {
		name      string
		tag       string
		planned   string
		releasing *semver.Version
		want      plan.Op
		empty     bool
	}{
		{"never pushed", "9.9.9", "sha256:new", nil, plan.Add, false},
		{"already the intended index", "1.2.0", same, nil, plan.Keep, false},
		{"different index", "1.2.0", "sha256:other", nil, plan.Change, false},
		{"floating tag behind the release", "1", "sha256:new", &releasing, plan.Change, false},
		{"floating tag already ahead", "2", "sha256:new", &releasing, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, err := observeTag(t.Context(), f.registry(), built, tt.tag, tt.planned, tt.releasing)
			if err != nil {
				t.Fatal(err)
			}
			if tt.empty {
				if action.Target != "" {
					t.Errorf("action = %+v, want none", action)
				}
				return
			}
			if action.Op != tt.want || action.Kind != plan.KindImage {
				t.Errorf("action = %+v, want op %q", action, tt.want)
			}
			if want := "ghcr.io/you/tool:" + tt.tag; action.Target != want {
				t.Errorf("target = %q, want %q", action.Target, want)
			}
		})
	}
}

func TestObserveImagesRejectsAVersionItCannotCompare(t *testing.T) {
	_, err := ObserveImages(t.Context(), []release.ImageBuild{{
		APIHost: "127.0.0.1:1", Repository: "you/tool", Version: "not-a-version", Floating: []string{"latest"},
	}}, "")
	if err == nil {
		t.Fatal("ObserveImages accepted a version it cannot compare")
	}
}
