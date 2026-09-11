package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var testTime = time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC)

func sample() []Entry {
	return []Entry{
		FromBytes("foo/README.md", false, []byte("# readme\n")),
		FromBytes("foo/foo", true, []byte("\x7fELF not really\n")),
		FromBytes("foo/LICENSE", false, []byte("MIT\n")),
	}
}

func digest(t *testing.T, f Format, entries []Entry, modTime time.Time) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, f, entries, modTime); err != nil {
		t.Fatalf("Write(%s): %v", f, err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// Entry order is whatever the caller discovered files in, which for a
// filesystem walk is not guaranteed stable across machines.
func TestEntryOrderDoesNotAffectOutput(t *testing.T) {
	for _, f := range []Format{FormatTarGz, FormatZip} {
		t.Run(string(f), func(t *testing.T) {
			forward := sample()
			reversed := sample()
			for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
				reversed[i], reversed[j] = reversed[j], reversed[i]
			}
			if a, b := digest(t, f, forward, testTime), digest(t, f, reversed, testTime); a != b {
				t.Errorf("entry order changed the archive:\n  %s\n  %s", a, b)
			}
		})
	}
}

// The same inputs written at two different moments must produce the same
// bytes. This is the property the whole tool rests on.
func TestWallClockDoesNotAffectOutput(t *testing.T) {
	for _, f := range []Format{FormatTarGz, FormatZip} {
		t.Run(string(f), func(t *testing.T) {
			first := digest(t, f, sample(), testTime)
			time.Sleep(1100 * time.Millisecond) // cross a whole-second boundary
			second := digest(t, f, sample(), testTime)
			if first != second {
				t.Errorf("archive changed between runs:\n  %s\n  %s", first, second)
			}
		})
	}
}

// Sub-second precision cannot be represented in a USTAR header or a DOS
// timestamp. Truncating in Write means two commit times within the same second
// cannot produce different archives.
func TestSubSecondPrecisionIsTruncated(t *testing.T) {
	coarse := time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC)
	fine := coarse.Add(750 * time.Millisecond)
	for _, f := range []Format{FormatTarGz, FormatZip} {
		t.Run(string(f), func(t *testing.T) {
			if a, b := digest(t, f, sample(), coarse), digest(t, f, sample(), fine); a != b {
				t.Errorf("sub-second precision leaked into the archive:\n  %s\n  %s", a, b)
			}
		})
	}
}

// A non-UTC instant must normalise to the same archive as its UTC equivalent,
// or the builder's timezone ends up in the extended-timestamp extra field.
func TestTimezoneDoesNotAffectOutput(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*60*60)
	for _, f := range []Format{FormatTarGz, FormatZip} {
		t.Run(string(f), func(t *testing.T) {
			utc := digest(t, f, sample(), testTime)
			shifted := digest(t, f, sample(), testTime.In(tokyo))
			if utc != shifted {
				t.Errorf("timezone changed the archive:\n  %s\n  %s", utc, shifted)
			}
		})
	}
}

// The umask in effect when a file was created must not reach the archive.
func TestDiskModeBitsAreNormalised(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("contents\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil { // WriteFile applies the umask
			t.Fatal(err)
		}
		return p
	}

	group := write("group", 0o640)
	world := write("world", 0o666)

	entryFor := func(p string) Entry {
		e, err := FromFile("pkg/file", p)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	for _, f := range []Format{FormatTarGz, FormatZip} {
		t.Run(string(f), func(t *testing.T) {
			a := digest(t, f, []Entry{entryFor(group)}, testTime)
			b := digest(t, f, []Entry{entryFor(world)}, testTime)
			if a != b {
				t.Errorf("disk mode bits reached the archive:\n  %s\n  %s", a, b)
			}
		})
	}
}

// Inspect the gzip header directly. These two fields are the reason an
// otherwise identical tar stream can still produce differing .tar.gz files on
// two machines.
func TestGzipHeaderCarriesNoTimestampOrPlatform(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatTarGz, sample(), testTime); err != nil {
		t.Fatal(err)
	}
	head := buf.Bytes()
	if len(head) < 10 {
		t.Fatalf("gzip output too short: %d bytes", len(head))
	}
	if mtime := binary.LittleEndian.Uint32(head[4:8]); mtime != 0 {
		t.Errorf("gzip MTIME = %d, want 0", mtime)
	}
	if osByte := head[9]; osByte != 255 {
		t.Errorf("gzip OS byte = %d, want 255 (unknown)", osByte)
	}
}

func TestTarHeadersArePinned(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatTarGz, sample(), testTime); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)

	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)

		if hdr.Uid != 0 || hdr.Gid != 0 {
			t.Errorf("%s: uid/gid = %d/%d, want 0/0", hdr.Name, hdr.Uid, hdr.Gid)
		}
		if hdr.Uname != "" || hdr.Gname != "" {
			t.Errorf("%s: uname/gname = %q/%q, want empty", hdr.Name, hdr.Uname, hdr.Gname)
		}
		if !hdr.ModTime.Equal(testTime) {
			t.Errorf("%s: modtime = %s, want %s", hdr.Name, hdr.ModTime, testTime)
		}
		if !hdr.AccessTime.IsZero() || !hdr.ChangeTime.IsZero() {
			t.Errorf("%s: atime/ctime should be zero, got %s/%s", hdr.Name, hdr.AccessTime, hdr.ChangeTime)
		}
		if hdr.Format != tar.FormatUSTAR {
			t.Errorf("%s: format = %s, want USTAR", hdr.Name, hdr.Format)
		}
		wantMode := int64(0o644)
		if hdr.Name == "foo/foo" {
			wantMode = 0o755
		}
		if hdr.Mode != wantMode {
			t.Errorf("%s: mode = %o, want %o", hdr.Name, hdr.Mode, wantMode)
		}
	}

	want := []string{"foo/LICENSE", "foo/README.md", "foo/foo"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("entries = %v, want %v (sorted)", names, want)
			break
		}
	}
}

func TestZipHeadersArePinned(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatZip, sample(), testTime); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"foo/LICENSE", "foo/README.md", "foo/foo"}
	if len(zr.File) != len(want) {
		t.Fatalf("got %d entries, want %d", len(zr.File), len(want))
	}
	for i, f := range zr.File {
		if f.Name != want[i] {
			t.Errorf("entry %d = %q, want %q (sorted)", i, f.Name, want[i])
		}
		if !f.Modified.Equal(testTime) {
			t.Errorf("%s: modified = %s, want %s", f.Name, f.Modified, testTime)
		}
		wantMode := os.FileMode(0o644)
		if f.Name == "foo/foo" {
			wantMode = 0o755
		}
		if f.Mode().Perm() != wantMode {
			t.Errorf("%s: mode = %o, want %o", f.Name, f.Mode().Perm(), wantMode)
		}
	}
}

func TestRejectsUnsafePaths(t *testing.T) {
	cases := map[string]string{
		"empty":     "",
		"absolute":  "/etc/passwd",
		"parent":    "../escape",
		"unclean":   "foo/./bar",
		"backslash": `foo\bar`,
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			err := Write(io.Discard, FormatTarGz, []Entry{FromBytes(p, false, nil)}, testTime)
			if err == nil {
				t.Errorf("Write(%q) succeeded, want an error", p)
			}
		})
	}
}

func TestRejectsDuplicatePaths(t *testing.T) {
	entries := []Entry{
		FromBytes("same", false, []byte("a")),
		FromBytes("same", false, []byte("b")),
	}
	if err := Write(io.Discard, FormatTarGz, entries, testTime); err == nil {
		t.Error("Write succeeded with duplicate paths, want an error")
	}
}

// A file that changes size between Stat and read would otherwise produce a
// corrupt archive rather than a failure.
func TestDetectsSizeMismatch(t *testing.T) {
	e := Entry{
		Path: "file",
		Size: 100,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("short"))), nil
		},
	}
	if err := Write(io.Discard, FormatTarGz, []Entry{e}, testTime); err == nil {
		t.Error("Write succeeded with a size mismatch, want an error")
	}
}
