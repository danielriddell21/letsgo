package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/install"
)

// executable finds the running letsgo. A variable so that a test can point it
// at a binary of its own.
var executable = os.Executable

// checkSelf reports whether the running letsgo is still the binary its
// install record describes, when install.sh or `letsgo update` left one
// beside it. go install and Homebrew leave none, and get no row: there is
// nothing recorded to compare against.
func (r *Result) checkSelf() {
	path, err := executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	rec, err := install.ReadRecord(path)
	if err != nil {
		r.add(toolsGroup, "letsgo", Warn, "reinstall with install.sh, or delete "+install.RecordPath(path), "%v", err)
		return
	}
	if rec == nil {
		return
	}

	got, err := fileSHA256(path)
	if err != nil {
		r.add(toolsGroup, "letsgo", Warn, "", "%s could not be read to compare with its install record: %v", path, err)
		return
	}
	if got != rec.BinarySHA256 {
		r.add(toolsGroup, "letsgo", Warn, "reinstall it, or run letsgo update",
			"%s is not the %s binary %s installed (sha256 %.12s, recorded %.12s)",
			path, rec.Tag, rec.Via, got, rec.BinarySHA256)
		return
	}
	r.add(toolsGroup, "letsgo", OK, "", "%s via %s, matching its install record (%s)", rec.Tag, rec.Via, path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("doctor: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("doctor: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
