package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// executable finds the running binary. A variable so that a test can exercise
// Apply without the test binary replacing itself to prove it works.
var executable = os.Executable

// Apply replaces the running executable with the update.
func (u *Update) Apply(ctx context.Context) error {
	target, err := executable()
	if err != nil {
		return fmt.Errorf("selfupdate: finding the running executable: %w", err)
	}
	return u.ApplyTo(ctx, resolve(target))
}

// resolve follows a symlink so that replacing a linked binary replaces the
// binary rather than the link. A path that cannot be resolved is used as it
// is: the update then fails on the real problem rather than on this.
func resolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// ApplyTo replaces the executable at path.
//
// Nothing is written until the download has been checked twice — once as an
// archive and once as the binary inside it — and the replacement itself is a
// rename, so an interrupted update leaves the old binary in place rather than
// half of a new one.
func (u *Update) ApplyTo(ctx context.Context, path string) error {
	binary, err := u.Download(ctx)
	if err != nil {
		return err
	}
	return replace(path, binary)
}

// Download fetches the update and returns the verified binary, without
// installing it. For a program that wants to put it somewhere of its own.
func (u *Update) Download(ctx context.Context) ([]byte, error) {
	resp, err := u.options.get(ctx, u.downloadURL)
	if err != nil {
		return nil, err
	}
	archive, err := read(resp)
	if err != nil {
		return nil, err
	}

	if got := digest(archive); got != u.SHA256 {
		return nil, fmt.Errorf(
			"selfupdate: %s is %s, but %s published %s — refusing to install it",
			u.Archive, short(got), u.Tag, short(u.SHA256))
	}

	binary, err := extract(archive, u.Archive, u.Binary)
	if err != nil {
		return nil, err
	}

	// Checked again: a correct archive containing the wrong binary is a thing
	// that can happen, and the manifest records both digests precisely so this
	// question can be asked.
	if u.BinarySHA256 != "" {
		if got := digest(binary); got != u.BinarySHA256 {
			return nil, fmt.Errorf(
				"selfupdate: the binary inside %s is %s, but %s published %s",
				u.Archive, short(got), u.Tag, short(u.BinarySHA256))
		}
	}
	return binary, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// extract pulls one named file out of a release archive.
func extract(data []byte, archiveName, binary string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return fromZip(data, binary)
	}
	return fromTarGz(data, binary)
}

func fromTarGz(data []byte, binary string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: reading archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("selfupdate: reading archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != binary {
			continue
		}

		out, err := io.ReadAll(io.LimitReader(tr, maxDownload))
		if err != nil {
			return nil, fmt.Errorf("selfupdate: reading %s: %w", binary, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("selfupdate: the archive contains no %s", binary)
}

func fromZip(data []byte, binary string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: reading archive: %w", err)
	}

	for _, f := range zr.File {
		if path.Base(f.Name) != binary {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("selfupdate: reading %s: %w", binary, err)
		}
		defer func() { _ = rc.Close() }()

		out, err := io.ReadAll(io.LimitReader(rc, maxDownload))
		if err != nil {
			return nil, fmt.Errorf("selfupdate: reading %s: %w", binary, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("selfupdate: the archive contains no %s", binary)
}

// replace swaps the file at target for the new binary.
//
// Written alongside the target rather than in a temporary directory, so the
// final step is a rename within one filesystem — which is atomic, and which a
// cross-device move would not be.
func replace(target string, binary []byte) error {
	dir := filepath.Dir(target)

	staged, err := os.CreateTemp(dir, "."+filepath.Base(target)+".new-*")
	if err != nil {
		return fmt.Errorf("selfupdate: %s is not writable: %w", dir, err)
	}
	staging := staged.Name()
	defer func() { _ = os.Remove(staging) }()

	if _, err := staged.Write(binary); err != nil {
		_ = staged.Close()
		return fmt.Errorf("selfupdate: writing the new binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("selfupdate: writing the new binary: %w", err)
	}
	// An updater that installs something the user cannot run has not updated
	// anything.
	if err := os.Chmod(staging, 0o755); err != nil { //nolint:gosec // a binary must be executable
		return fmt.Errorf("selfupdate: %w", err)
	}

	// Windows will not let a running executable be overwritten, but it will
	// let one be renamed. Moving the old binary aside frees the name; the
	// leftover is removed on the next update, when it is no longer running.
	previous := ""
	if runtime.GOOS == "windows" {
		previous = target + ".old"
		_ = os.Remove(previous)
		if err := os.Rename(target, previous); err != nil {
			return fmt.Errorf("selfupdate: moving the running binary aside: %w", err)
		}
	}

	if err := os.Rename(staging, target); err != nil {
		if previous != "" {
			// Put it back: a failed update must not leave the program
			// uninstalled.
			_ = os.Rename(previous, target)
		}
		return fmt.Errorf("selfupdate: replacing %s: %w", target, err)
	}

	if previous != "" {
		_ = os.Remove(previous)
	}
	return nil
}
