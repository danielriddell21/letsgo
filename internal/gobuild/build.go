// Package gobuild invokes the Go toolchain with the flags and environment a
// reproducible release requires.
//
// The build environment is constructed from an allowlist rather than inherited.
// Anything the caller's shell happens to have set — GOFLAGS, GOEXPERIMENT, a
// vendored CGO toolchain — is a potential source of variance between two
// machines building the same commit, so it does not reach the child process
// unless it is named here.
package gobuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

// Target is a GOOS/GOARCH pair.
type Target struct {
	OS   string
	Arch string
}

func (t Target) String() string { return t.OS + "/" + t.Arch }

// Ext returns the executable suffix for the target.
func (t Target) Ext() string {
	if t.OS == "windows" {
		return ".exe"
	}
	return ""
}

// Host returns the target the current process is running on.
func Host() Target { return Target{OS: runtime.GOOS, Arch: runtime.GOARCH} }

// Request describes one binary to build.
type Request struct {
	// Dir is the module directory to build from.
	Dir string

	// Package is the package to build, e.g. "./cmd/foo".
	Package string

	// Output is the path to write the binary to.
	Output string

	// Target selects GOOS and GOARCH.
	Target Target

	// LDFlags are appended after the default "-s -w", typically -X assignments.
	LDFlags []string

	// GoBin overrides the toolchain binary. Defaults to "go" on PATH.
	GoBin string
}

// Build compiles a single binary.
func Build(ctx context.Context, req Request) error {
	if req.Dir == "" {
		return fmt.Errorf("gobuild: Dir is required")
	}
	if req.Package == "" {
		return fmt.Errorf("gobuild: Package is required")
	}
	if req.Output == "" {
		return fmt.Errorf("gobuild: Output is required")
	}

	gobin := req.GoBin
	if gobin == "" {
		gobin = "go"
	}

	ldflags := append([]string{"-s", "-w"}, req.LDFlags...)

	args := []string{
		"build",

		// Without -trimpath the absolute path of the build directory is
		// recorded in the binary, so the same source built in two different
		// checkouts produces different bytes.
		"-trimpath",

		// VCS stamping records the commit and a dirty flag by inspecting the
		// surrounding .git directory. That makes the output depend on whether
		// a .git directory is present, which breaks rebuilding from a source
		// archive — precisely what verification needs to do. Version metadata
		// is injected through -X instead, where it is an explicit input we
		// record rather than an ambient one we hope matches.
		"-buildvcs=false",

		"-ldflags", strings.Join(ldflags, " "),
		"-o", req.Output,
		req.Package,
	}

	cmd := exec.CommandContext(ctx, gobin, args...)
	cmd.Dir = req.Dir
	cmd.Env = environ(req.Target)

	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr

	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(stderr.String())
		if out == "" {
			return fmt.Errorf("gobuild: building %s for %s: %w", req.Package, req.Target, err)
		}
		return fmt.Errorf("gobuild: building %s for %s: %w\n%s", req.Package, req.Target, err, out)
	}
	return nil
}

// passthrough names the environment variables the toolchain genuinely needs:
// where to find itself, where to cache, and how to reach a module proxy.
// Everything else is dropped.
//
// None of these can change the bytes the compiler emits. They decide where the
// toolchain looks for things, not what it produces — which is the line that
// separates a variable worth passing through from one worth dropping.
var passthrough = []string{
	// Locating the toolchain and its caches.
	"PATH",
	"HOME",
	"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN",

	// Scratch space.
	"TMPDIR", "TMP", "TEMP",

	// Module resolution and verification.
	"GOPROXY", "GONOPROXY", "GOPRIVATE", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",

	// Windows. These are not optional extras: with GOCACHE unset, the
	// toolchain derives the build cache location from %LocalAppData%, and
	// without it refuses to build at all. An allowlist strict enough to
	// protect determinism is also strict enough to break the toolchain, so
	// each platform's genuine requirements have to be named explicitly.
	"USERPROFILE", "LOCALAPPDATA", "APPDATA",
	"SystemRoot", "windir", "ComSpec", "PATHEXT",
	"HOMEDRIVE", "HOMEPATH",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE",
}

func environ(t Target) []string {
	env := make(map[string]string, len(passthrough)+6)

	for _, key := range passthrough {
		if v, ok := os.LookupEnv(key); ok {
			env[key] = v
		}
	}

	env["GOOS"] = t.OS
	env["GOARCH"] = t.Arch

	// Cgo makes the output depend on a C toolchain we neither control nor
	// record, so it is off unless a future release makes it an explicit,
	// recorded decision.
	env["CGO_ENABLED"] = "0"

	// Both of these silently alter compilation. Empty means "the toolchain
	// default", which is what we want; inheriting means "whatever this shell
	// happened to have".
	env["GOFLAGS"] = ""
	env["GOEXPERIMENT"] = ""

	// Locale can affect tool output formatting. Pin it for good measure.
	env["LC_ALL"] = "C"

	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	// Sorted so the environment itself is reproducible, which matters when a
	// build failure is being compared between two runs.
	sort.Strings(out)
	return out
}

// Version reports the toolchain version string, e.g. "go1.24.7". It is a build
// input and belongs in the release manifest.
func Version(ctx context.Context, goBin string) (string, error) {
	if goBin == "" {
		goBin = "go"
	}
	out, err := exec.CommandContext(ctx, goBin, "env", "GOVERSION").Output()
	if err != nil {
		return "", fmt.Errorf("gobuild: reading GOVERSION: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
