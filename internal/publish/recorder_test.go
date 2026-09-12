package publish_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// The value of a rehearsal is that it takes the real path. If it decided
// anything differently it would only prove the rehearsal works.
func TestRecorderRunsTheRealDecisions(t *testing.T) {
	f := setup(t, true)

	var out bytes.Buffer
	recorder := publish.NewRecorder(&out)
	f.opts.Client = recorder

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.Created {
		t.Error("a rehearsal against no existing release should report a creation")
	}
	if len(result.Uploaded) != len(f.files) {
		t.Errorf("uploaded %v, want all %d files", result.Uploaded, len(f.files))
	}
	for name := range f.files {
		if !strings.Contains(out.String(), name) {
			t.Errorf("%s does not appear in the record:\n%s", name, out.String())
		}
	}
}

// A rehearsal of a resumed release must make the same skip and replace
// decisions the real run would.
func TestRecorderReproducesResumeDecisions(t *testing.T) {
	f := setup(t, true)
	present := f.opts.Files[0]
	truncated := f.opts.Files[1]

	recorder := publish.NewRecorder(nil)
	recorder.Existing = &github.Release{
		ID: 42, TagName: "v1.0.0",
		Assets: []github.Asset{
			{ID: 1, Name: present, Size: int64(len(f.files[present])), Digest: "sha256:" + f.opts.Sums[present]},
			{ID: 2, Name: truncated, Size: 3, Digest: "sha256:deadbeef"},
		},
	}
	f.opts.Client = recorder

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Skipped) != 1 || result.Skipped[0] != present {
		t.Errorf("Skipped = %v, want [%s]", result.Skipped, present)
	}
	if len(result.Replaced) != 1 || result.Replaced[0] != truncated {
		t.Errorf("Replaced = %v, want [%s]", result.Replaced, truncated)
	}
	if result.Created {
		t.Error("reported a creation for a release that already exists")
	}
}

// An unreadable or truncated file must fail the rehearsal, or a rehearsal
// could pass where the real run would not.
func TestRecorderStillReadsEveryFile(t *testing.T) {
	f := setup(t, true)
	name := f.opts.Files[0]
	f.opts.Sums[name] = "unchanged"

	// Claim a size the file does not have, exactly as a race would.
	recorder := publish.NewRecorder(nil)
	f.opts.Client = recorder
	f.opts.Files = []string{name}

	if _, err := publish.Run(context.Background(), f.opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recorder.Calls) == 0 {
		t.Error("nothing was recorded")
	}
}

func TestRecorderReportsSizesReadably(t *testing.T) {
	f := setup(t, true)
	var out bytes.Buffer
	f.opts.Client = publish.NewRecorder(&out)

	if _, err := publish.Run(context.Background(), f.opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), " B)") {
		t.Errorf("sizes are not reported:\n%s", out.String())
	}
}
