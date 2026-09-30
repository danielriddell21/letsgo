package receipt_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/receipt"
	"github.com/danielriddell21/letsgo/internal/verify"
)

var printed = time.Date(2026, 9, 30, 14, 5, 0, 0, time.UTC)

const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func result(status verify.Status, checks ...verify.Check) *verify.Result {
	m := &manifest.Manifest{}
	m.Builder.Go = "go1.27.1"
	r := &verify.Result{
		Repo: "acme/tool", Tag: "v1.2.3", Manifest: m,
		Artifacts: []verify.ArtifactResult{
			{Name: "tool_1.2.3_linux_amd64.tar.gz", Size: 1_250_000, SHA256: sum, Status: status},
		},
		Checks: checks,
	}
	return r
}

func TestRenderAPassingVerification(t *testing.T) {
	r := result(verify.Pass, verify.Check{Name: "provenance", Status: verify.Pass})

	want := `╔══════════════════════════════════════╗
║            SHIP IT & SAVE            ║
╚══════════════════════════════════════╝
acme/tool                         v1.2.3
2026-09-30 14:05                go1.27.1
----------------------------------------
tool_1.2.3_linux_amd64.tar.gz
  1.2 MB  01234567…cdef          ✓ MATCH
----------------------------------------
ITEMS                                  1
BYTES DIFFERING                        0
PROVENANCE                    ✓ ATTESTED
----------------------------------------
TOTAL                      BIT-FOR-BIT ✓
odds of an accidental match: 1 in 2²⁵⁶

  thank you for verifying. come again
`
	if got := receipt.Render(r, printed); got != want {
		t.Errorf("receipt:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderAFailingVerificationIsVoid(t *testing.T) {
	r := result(verify.Fail,
		verify.Check{Name: "rebuild", Status: verify.Fail},
		verify.Check{Name: "provenance", Status: verify.Warn})

	got := receipt.Render(r, printed)
	for _, want := range []string{
		"✗ MISMATCH", "BYTES DIFFERING                        1",
		"! NOT ATTESTED", "TOTAL                               VOID", "VOID — DO NOT ACCEPT",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("receipt is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BIT-FOR-BIT") {
		t.Errorf("a failed verification claims a match:\n%s", got)
	}
}

func TestRenderShowsChecksThatDidNotRunAsSkipped(t *testing.T) {
	got := receipt.Render(result(verify.Skip), printed)

	if !strings.Contains(got, "— SKIPPED") {
		t.Errorf("an artifact nothing checked is not shown as skipped:\n%s", got)
	}
	if strings.Count(got, "— SKIPPED") != 2 {
		t.Errorf("want the artifact and the missing provenance check skipped:\n%s", got)
	}
}

func TestRenderShowsImagesOnlyWhenChecked(t *testing.T) {
	without := receipt.Render(result(verify.Pass), printed)
	if strings.Contains(without, "IMAGES") {
		t.Errorf("an image line appears without an image check:\n%s", without)
	}

	with := receipt.Render(result(verify.Pass, verify.Check{Name: "images", Status: verify.Pass}), printed)
	if !strings.Contains(with, "IMAGES                           ✓ MATCH") {
		t.Errorf("image check is missing:\n%s", with)
	}
}

func TestRenderNeverExceedsTheReceiptWidth(t *testing.T) {
	r := result(verify.Fail, verify.Check{Name: "rebuild", Status: verify.Fail})
	r.Repo = "a-very-long-organisation-name/an-even-longer-repository-name"
	r.Tag = "services/payments/gateway/v10.20.30-beta.4"
	r.Artifacts[0].Name = "an-extraordinarily-long-artifact-name_10.20.30_linux_amd64.tar.gz"
	r.Artifacts[0].Size = 12
	r.Artifacts = append(r.Artifacts, verify.ArtifactResult{Name: "tool", Size: 42_000, SHA256: sum, Status: verify.Pass})

	for _, line := range strings.Split(receipt.Render(r, printed), "\n") {
		if n := utf8.RuneCountInString(line); n > 40 {
			t.Errorf("%d columns, want at most 40: %q", n, line)
		}
	}
	if got := receipt.Render(r, printed); !strings.Contains(got, "an-extraordinarily-l…_linux_amd64.tar.gz") {
		t.Errorf("long name is not shortened from the middle:\n%s", got)
	}
}

func TestReceiptMatchesVerify(t *testing.T) {
	for _, status := range []verify.Status{verify.Pass, verify.Warn, verify.Skip, verify.Fail} {
		r := result(verify.Pass, verify.Check{Name: "rebuild", Status: status})

		got := receipt.Render(r, printed)
		if ok := strings.Contains(got, "BIT-FOR-BIT ✓"); ok != r.OK() {
			t.Errorf("check %s: receipt says match = %v, verify says OK = %v", status, ok, r.OK())
		}
		if void := strings.Contains(got, "VOID — DO NOT ACCEPT"); void == r.OK() {
			t.Errorf("check %s: stamp = %v but OK = %v", status, void, r.OK())
		}
	}
}
