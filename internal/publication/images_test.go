package publication

import (
	"io"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/release"
)

func TestPublishImagesHasNothingToDoWithoutImages(t *testing.T) {
	t.Parallel()
	var out strings.Builder

	err := publishImages(t.Context(), &out, Options{Plan: releasePlan(), Result: &release.Result{}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("reported %q with no images", out.String())
	}
}

func TestPublishImagesDescribesWhatARehearsalWouldPush(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	o := Options{Plan: releasePlan(), Result: &release.Result{Images: []release.ImageBuild{{Registry: "ghcr.io", Repository: "you/foo", Tags: []string{"1.2.3"}}}}, Snapshot: true}

	if err := publishImages(t.Context(), &out, o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "images that would be pushed") {
		t.Errorf("a rehearsal did not describe its images:\n%s", out.String())
	}
}

func TestPublishImagesRefusesAFloatingTagItCannotCompare(t *testing.T) {
	t.Parallel()
	o := Options{Plan: releasePlan(), Result: &release.Result{Images: []release.ImageBuild{unversionedImage()}}}

	if err := publishImages(t.Context(), io.Discard, o); err == nil {
		t.Error("an image with floating tags and no version was pushed")
	}
}

// unversionedImage is an image whose floating tags cannot be compared with
// anything, which every push and every observation refuses before it reaches
// a registry.
func unversionedImage() release.ImageBuild {
	return release.ImageBuild{
		Registry: "ghcr.io", Repository: "you/foo", Tags: []string{"1.2.3"},
		Floating: []string{"latest"}, Version: "not-a-version",
	}
}
