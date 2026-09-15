package zig

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/safeexec"
)

// maxEntry bounds one file extracted from the tarball. The archive is verified
// against its digest before this runs, so this guards against a pinned but
// malformed archive rather than a hostile one.
const maxEntry = 1 << 30

// archiveName is the published file's name, which carries its own format:
// Windows builds are zip, everything else is tar.xz.
func archiveName(version, host string) string {
	if strings.HasSuffix(host, "-windows") {
		return fmt.Sprintf("zig-%s-%s.zip", host, version)
	}
	return fmt.Sprintf("zig-%s-%s.tar.xz", host, version)
}

// extract unpacks the archive into dir, flattening the single directory the
// tarball wraps everything in.
//
// Written to a temporary directory and renamed, so that an interrupted
// extraction cannot leave a half-unpacked toolchain that the next run mistakes
// for a complete one.
func extract(ctx context.Context, archive, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("zig: %w", err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(dir), ".zig-*")
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if strings.HasSuffix(archive, ".zip") {
		err = unzip(archive, staging)
	} else {
		err = untar(ctx, archive, staging)
	}
	if err != nil {
		return err
	}

	root, err := singleDir(staging)
	if err != nil {
		return err
	}
	if err := os.Rename(root, dir); err != nil {
		// Another release may have extracted the same toolchain first, which
		// is a race worth losing rather than failing over.
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil
		}
		return fmt.Errorf("zig: %w", err)
	}
	return nil
}

// untar shells out, because the tarball is xz-compressed and the standard
// library has no xz decoder.
//
// Consistent with how letsgo already reaches apidiff and govulncheck: an
// external binary invoked for one job, rather than a dependency in go.mod. tar
// reads xz on Linux, macOS and Windows 10 and later.
func untar(ctx context.Context, archive, dir string) error {
	// Both paths are ones this package built — a temporary file it downloaded
	// into, and a staging directory under the cache — but neither is written
	// down here, so they are checked rather than assumed. exec runs tar
	// directly with no shell, so the remaining risk is a relative or unclean
	// path meaning something other than it appears to.
	archive, err := checkedPath(archive)
	if err != nil {
		return err
	}
	dir, err = checkedPath(dir)
	if err != nil {
		return err
	}

	// Found in a system directory rather than through PATH, and run with a
	// fixed PATH of its own, so that a writable directory ahead of /usr/bin
	// cannot decide which program unpacks the toolchain. This is the same
	// treatment git gets, for the same reason.
	tar, err := safeexec.LookIn(safeexec.SystemDirs(), safeexec.Exe("tar"))
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}

	// "--" so that a path starting with a dash is an operand rather than an
	// option, whatever tar's argument parser would otherwise make of it.
	cmd := exec.CommandContext(ctx, tar, "-x", "-f", archive, "-C", dir, "--")
	cmd.Env = safeexec.EnvWithFixedPath(os.Environ())

	out, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("zig: extracting %s: %w\n%s", filepath.Base(archive), err, detail)
		}
		return fmt.Errorf("zig: extracting %s: %w", filepath.Base(archive), err)
	}
	return nil
}

// checkedPath proves a path is absolute and in its simplest form before it is
// handed to another program.
func checkedPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("zig: %q is not an absolute path", path)
	}
	if filepath.Clean(path) != path {
		return "", fmt.Errorf("zig: %q is not a cleaned path", path)
	}
	return path, nil
}

func unzip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		if err := writeZipEntry(f, dir); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, dir string) error {
	// An entry naming a path outside the destination would write wherever it
	// liked. The archive is pinned, so this cannot happen — which is exactly
	// why it is cheap to refuse rather than reason about.
	path := filepath.Join(dir, filepath.FromSlash(f.Name)) //nolint:gosec // checked below
	if !strings.HasPrefix(path, filepath.Clean(dir)+string(os.PathSeparator)) {
		return fmt.Errorf("zig: archive entry %q escapes the destination", f.Name)
	}

	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return fmt.Errorf("zig: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("zig: %w", err)
	}

	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	defer func() { _ = src.Close() }()

	mode := f.Mode().Perm() | 0o600
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	if _, err := io.Copy(dst, io.LimitReader(src, maxEntry)); err != nil {
		_ = dst.Close()
		return fmt.Errorf("zig: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	return nil
}

// singleDir returns the one directory the archive wraps its contents in.
func singleDir(staging string) (string, error) {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return "", fmt.Errorf("zig: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", fmt.Errorf("zig: the archive does not hold a single root directory")
	}
	return filepath.Join(staging, entries[0].Name()), nil
}
