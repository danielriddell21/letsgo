package plan

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/oci"
)

func imageTarget(t *testing.T, base string) *ImageTarget {
	t.Helper()
	ref, err := oci.ParseReference(base)
	if err != nil {
		t.Fatal(err)
	}
	return &ImageTarget{
		Registry: "ghcr.io", Repository: "you/tool", Base: ref,
		Platforms: []gobuild.Target{{OS: "linux", Arch: "amd64"}},
	}
}

// A registry that answered is reporting a problem with the reference, and
// finding that out in two seconds is the whole reason to resolve while
// planning rather than while building.
func TestBaseResultFailsWhenTheRegistryAnswers(t *testing.T) {
	target := imageTarget(t, "gcr.io/distroless/static:nonroot")
	err := &oci.Error{StatusCode: 404, Method: "GET", URL: "https://gcr.io/v2/...", Body: "MANIFEST_UNKNOWN"}

	status, detail := baseResult("ghcr.io/you/tool", target, nil, fmt.Errorf("oci: reading base: %w", err))
	if status != Fail {
		t.Errorf("status = %s, want fail", status)
	}
	if !strings.Contains(detail, "MANIFEST_UNKNOWN") {
		t.Errorf("detail = %q, want the registry's own words", detail)
	}
}

// Being offline says nothing about the config, and planning without a network
// is worth keeping.
func TestBaseResultWarnsWhenTheRegistryIsUnreachable(t *testing.T) {
	target := imageTarget(t, "gcr.io/distroless/static:nonroot")
	err := fmt.Errorf("oci: reading base: %w", &net.OpError{Op: "dial", Err: errors.New("no such host")})

	status, detail := baseResult("ghcr.io/you/tool", target, nil, err)
	if status != Warn {
		t.Errorf("status = %s, want warn", status)
	}
	if !strings.Contains(detail, "was not checked") {
		t.Errorf("detail = %q", detail)
	}
}

// The advice is only actionable if it names the digest to pin.
func TestBaseResultNamesTheDigestATagResolvedTo(t *testing.T) {
	target := imageTarget(t, "gcr.io/distroless/static:nonroot")
	base := &oci.Base{IndexDigest: "sha256:afa5c872", Digest: "sha256:platform"}

	status, detail := baseResult("ghcr.io/you/tool", target, base, nil)
	if status != Warn {
		t.Errorf("status = %s, want warn", status)
	}
	if !strings.Contains(detail, "pin it with @sha256:afa5c872") {
		t.Errorf("detail = %q, want the resolved index digest", detail)
	}
}

// A reference already pinned to a digest has nothing left to warn about.
func TestBaseResultPassesAPinnedBase(t *testing.T) {
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	target := imageTarget(t, "gcr.io/distroless/static:nonroot@"+digest)

	status, detail := baseResult("ghcr.io/you/tool", target, &oci.Base{IndexDigest: oci.Digest(digest)}, nil)
	if status != Pass {
		t.Errorf("status = %s, want pass: %s", status, detail)
	}
	if strings.Contains(detail, "pin it") {
		t.Errorf("detail = %q, want no advice", detail)
	}
}
