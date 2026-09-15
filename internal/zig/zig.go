// Package zig obtains the C toolchain a cgo release compiles with.
//
// cgo is reproducible; what makes it look otherwise is that the C compiler is
// an unrecorded build input. Two compilers produce different bytes from the
// same source, so a release built with whatever `cc` the machine happened to
// have cannot be reproduced anywhere else.
//
// Zig is used because it solves that in one artifact: a single tarball,
// published with a digest, containing a cross-compiler and its own libc and
// headers. Pin the tarball and the sysroot is pinned with it, so the host's
// own toolchain never reaches the output — which is the property that lets
// someone else's machine reproduce the build.
//
// The digests below are in letsgo's source rather than fetched, so the pin is
// part of the commit that released letsgo rather than a file on a web server
// that can change.
package zig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// Default is the version letsgo uses when a repository does not name one.
const Default = "0.16.0"

// release is one published tarball.
type release struct {
	// Host is zig's name for the platform it runs on, which is also part of
	// the tarball's name.
	Host   string
	SHA256 string
}

// known maps a version and a host to the tarball's digest.
//
// Recorded here rather than read from ziglang.org's index: the index is
// mutable, and a digest fetched at build time proves only that the download
// matched what the server said today.
var known = map[string]map[string]release{
	"0.16.0": {
		"linux/amd64":   {"x86_64-linux", "70e49664a74374b48b51e6f3fdfbf437f6395d42509050588bd49abe52ba3d00"},
		"linux/arm64":   {"aarch64-linux", "ea4b09bfb22ec6f6c6ceac57ab63efb6b46e17ab08d21f69f3a48b38e1534f17"},
		"darwin/amd64":  {"x86_64-macos", "0387557ed1877bc6a2e1802c8391953baddba76081876301c522f52977b52ba7"},
		"darwin/arm64":  {"aarch64-macos", "b23d70deaa879b5c2d486ed3316f7eaa53e84acf6fc9cc747de152450d401489"},
		"windows/amd64": {"x86_64-windows", "68659eb5f1e4eb1437a722f1dd889c5a322c9954607f5edcf337bc3684a75a7e"},
		"windows/arm64": {"aarch64-windows", "aee38316ee4111717900f45dd3130145c39289e105541d737eb8c5ed653c78ef"},
	},
}

// Versions lists the versions letsgo knows digests for.
func Versions() []string {
	out := make([]string, 0, len(known))
	for version := range known {
		out = append(out, version)
	}
	return out
}

// targets maps a Go target to zig's name for it.
//
// musl rather than glibc: it links statically, which is what a release binary
// should be and what letsgo's CGO_ENABLED=0 builds already are. A glibc target
// would additionally have to pin a glibc version, making the binary refuse to
// run on anything older.
var targets = map[string]string{
	"linux/amd64":   "x86_64-linux-musl",
	"linux/arm64":   "aarch64-linux-musl",
	"linux/arm":     "arm-linux-musleabihf",
	"windows/amd64": "x86_64-windows-gnu",
	"windows/arm64": "aarch64-windows-gnu",
}

// Target returns zig's name for a Go target, and whether cgo can be
// cross-compiled to it.
//
// darwin is absent deliberately. Zig carries libc for macOS but not Apple's
// frameworks, and Go's darwin runtime links CoreFoundation, so a cgo build for
// darwin needs a macOS host with the SDK installed. Saying that plainly is
// better than emitting a link error nobody can read.
func Target(t gobuild.Target) (string, bool) {
	triple, ok := targets[t.String()]
	return triple, ok
}

// Toolchain is an obtained zig, ready to compile with.
type Toolchain struct {
	Version string

	// Digest is the SHA-256 of the tarball it came from, as "sha256:…". This
	// is what the manifest records: it identifies the compiler exactly, the
	// way the Go toolchain version does.
	Digest string

	// Path is the zig executable.
	Path string
}

// baseline pins the instruction set the compiler may use.
//
// Without it zig picks the CPU by comparing the target to the host: an
// architecture that matches the machine it is running on is treated as native
// and compiled with whatever features that machine has. Two runners then
// produce different binaries for the same target — an x86_64 Windows runner
// emits native code for x86_64-linux while an arm64 Mac cross-compiles the
// same target at baseline, and the two differ by kilobytes.
//
// That is a build depending on the machine, which is the whole thing letsgo
// exists to prevent. Saying "baseline" makes the answer the same everywhere,
// at the cost of the instruction set extensions a release binary should not
// assume anyway.
const baseline = " -mcpu=baseline"

// CC returns the compiler command for a target, as a full command line.
func (t Toolchain) CC(target gobuild.Target) (string, bool) {
	triple, ok := Target(target)
	if !ok {
		return "", false
	}
	return t.Path + " cc -target " + triple + baseline, true
}

// CXX returns the C++ compiler command for a target.
func (t Toolchain) CXX(target gobuild.Target) (string, bool) {
	triple, ok := Target(target)
	if !ok {
		return "", false
	}
	return t.Path + " c++ -target " + triple + baseline, true
}

// Ensure obtains the toolchain, downloading it once and reusing it after.
//
// digest overrides the built-in pin, for a version letsgo does not ship one
// for. Empty means use the built-in, and a version with neither is refused
// rather than downloaded unverified.
func Ensure(ctx context.Context, version, digest, cacheDir string) (Toolchain, error) {
	if version == "" {
		version = Default
	}

	host := gobuild.Host().String()
	entry, ok := known[version][host]
	switch {
	case ok && digest == "":
		digest = entry.SHA256
	case !ok && digest == "":
		return Toolchain{}, fmt.Errorf(
			"zig: letsgo has no digest for %s on %s; pin one with `cgo zig %s sha256:…`, "+
				"or use a version it knows (%s)",
			version, host, version, strings.Join(Versions(), ", "))
	case !ok:
		// A digest was given, so the version need not be one letsgo ships, but
		// the host still has to be one zig publishes for.
		if entry.Host = hostName(); entry.Host == "" {
			return Toolchain{}, fmt.Errorf("zig: no zig build is published for %s", host)
		}
	}
	digest = strings.TrimPrefix(digest, sha256Prefix)

	dir := filepath.Join(cacheDir, "zig", version+"-"+digest[:12])
	exe := filepath.Join(dir, "zig"+exeSuffix())

	// The same toolchain either way: whether it was already on disk or has
	// just been unpacked says nothing about what it is.
	toolchain := Toolchain{Version: version, Digest: sha256Prefix + digest, Path: exe}

	if _, err := os.Stat(exe); err == nil {
		return toolchain, nil
	}

	if err := download(ctx, version, entry.Host, digest, dir); err != nil {
		return Toolchain{}, err
	}
	if _, err := os.Stat(exe); err != nil {
		return Toolchain{}, fmt.Errorf("zig: %s did not contain a zig executable", version)
	}
	return toolchain, nil
}

// sha256Prefix is how a digest is written everywhere it is read or recorded,
// so that trimming it and putting it back cannot drift apart.
const sha256Prefix = "sha256:"

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// hostName is zig's name for the platform letsgo is running on.
func hostName() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	goos := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[runtime.GOOS]
	if arch == "" || goos == "" {
		return ""
	}
	return arch + "-" + goos
}

// download fetches the tarball, proves it is the pinned one, and extracts it.
//
// Verified before extraction rather than after: extracting first would mean
// writing an unverified archive's contents to disk and then deciding whether
// to trust them.
// downloadBase is where the tarballs live. A variable so that tests can serve
// their own, never so that a release can: the digest is what decides whether
// the bytes are acceptable, wherever they came from.
var downloadBase = "https://ziglang.org/download"

func download(ctx context.Context, version, host, digest, dir string) error {
	name := archiveName(version, host)
	url := fmt.Sprintf("%s/%s/%s", downloadBase, version, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	req.Header.Set("User-Agent", "letsgo")

	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("zig: fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zig: fetching %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp("", "letsgo-zig-*"+filepath.Ext(name))
	if err != nil {
		return fmt.Errorf("zig: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, sum), resp.Body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("zig: downloading %s: %w", url, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("zig: %w", err)
	}

	if got := hex.EncodeToString(sum.Sum(nil)); got != digest {
		return fmt.Errorf("zig: %s is %s, but the pin says %s", url, got[:12], digest[:12])
	}
	return extract(ctx, tmp.Name(), dir)
}
