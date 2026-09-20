package release

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

// The manifest records the base a release actually built on, and a base the
// config already pinned by digest is the ordinary case: it is what Dependabot
// and Renovate write. Appending the digest to a reference that carries one
// produced "repo@sha256:x@sha256:x", which names no image at all.
func TestImageRecordsPinTheBaseOnce(t *testing.T) {
	const digest = "sha256:afa5c872f7ab"

	tests := []struct {
		name string
		base oci.Reference
		want string
	}{
		{
			name: "written as a tag",
			base: oci.Reference{Registry: "gcr.io", Repository: "distroless/static-debian12", Tag: "latest"},
			want: "gcr.io/distroless/static-debian12@" + digest,
		},
		{
			name: "written as a digest",
			base: oci.Reference{
				Registry: "gcr.io", Repository: "distroless/static-debian12",
				Digest: "sha256:something-older",
			},
			want: "gcr.io/distroless/static-debian12@" + digest,
		},
		{
			name: "written with neither",
			base: oci.Reference{Registry: "gcr.io", Repository: "distroless/static-debian12"},
			want: "gcr.io/distroless/static-debian12@" + digest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := imageRecords([]ImageBuild{{
				Registry:   "ghcr.io",
				Repository: "you/tool",
				Tags:       []string{"1.0.0"},
				Base:       &oci.Base{Reference: tt.base, IndexDigest: digest},
			}})

			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			if got := records[0].Base; got != tt.want {
				t.Errorf("Base = %q, want %q", got, tt.want)
			}
		})
	}
}

// A release with no base image records none, rather than a bare digest.
func TestImageRecordsLeaveTheBaseEmptyWhenThereIsNone(t *testing.T) {
	records := imageRecords([]ImageBuild{{
		Registry: "ghcr.io", Repository: "you/tool", Tags: []string{"1.0.0"},
	}})
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Base != "" {
		t.Errorf("Base = %q, want empty", records[0].Base)
	}
}
