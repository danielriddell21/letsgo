package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/install"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

// fakePlugin writes an executable that only doctor's digest check ever
// reads, and returns its digest, mirroring internal/plugin/plugin_test.go's
// own fake helper.
func fakePlugin(t *testing.T) (dir, digest string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}

	dir = t.TempDir()
	path := filepath.Join(dir, "letsgo-fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '{}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return dir, "sha256:" + hex.EncodeToString(sum[:])
}

func TestVersionSatisfies(t *testing.T) {
	cases := map[string]struct {
		want  string
		exact bool
		got   string
		ok    bool
	}{
		"toolchain exact match":                     {"go1.24.7", true, "go1.24.7", true},
		"toolchain patch mismatch":                  {"go1.24.7", true, "go1.24.8", false},
		"toolchain newer local still switches":      {"go1.24.7", true, "go1.25.0", false},
		"go directive satisfied by newer patch":     {"go1.24", false, "go1.24.7", true},
		"go directive satisfied by newer minor":     {"go1.24", false, "go1.27.1", true},
		"go directive satisfied by exact match":     {"go1.24.7", false, "go1.24.7", true},
		"go directive not satisfied by older":       {"go1.28", false, "go1.27.1", false},
		"go directive not satisfied by older patch": {"go1.24.7", false, "go1.24.6", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := versionSatisfies(tc.want, tc.exact, tc.got); got != tc.ok {
				t.Errorf("versionSatisfies(%q, %v, %q) = %v, want %v", tc.want, tc.exact, tc.got, got, tc.ok)
			}
		})
	}
}

func TestCheckGateToolReportsAMissingToolAsWarn(t *testing.T) {
	r := &Result{}
	r.checkGateTool(nil, "letsgo-doctor-test-nonexistent-tool", "install it somehow", false)

	if len(r.Checks) != 1 {
		t.Fatalf("Checks = %d, want 1", len(r.Checks))
	}
	c := r.Checks[0]
	if c.Status != Warn {
		t.Errorf("Status = %v, want Warn", c.Status)
	}
	if c.Hint != "install it somehow" {
		t.Errorf("Hint = %q, want the install command", c.Hint)
	}
	if !r.OK() {
		t.Error("OK() = false, want true: a Warn alone must not fail the run")
	}
}

func TestCheckGateToolEscalatesToFailWhenRequired(t *testing.T) {
	r := &Result{}
	r.checkGateTool(nil, "letsgo-doctor-test-nonexistent-tool", "install it somehow", true)

	if r.Checks[0].Status != Fail {
		t.Errorf("Status = %v, want Fail", r.Checks[0].Status)
	}
	if r.OK() {
		t.Error("OK() = true, want false with a Fail check present")
	}
}

func TestCheckPluginFindsAMatchOnPath(t *testing.T) {
	t.Setenv(pluginstore.StoreEnvOverride, t.TempDir())
	dir, digest := fakePlugin(t)
	t.Setenv("PATH", dir)

	r := &Result{}
	r.checkPlugin(config.Plugin{Command: "letsgo-fake", Version: "v1.0.0", Digest: digest}, ".", "")

	if len(r.Checks) != 1 {
		t.Fatalf("Checks = %d, want 1", len(r.Checks))
	}
	c := r.Checks[0]
	if c.Status != OK {
		t.Errorf("Status = %v, want OK: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "letsgo-fake") {
		t.Errorf("Detail = %q, want it to name the resolved path", c.Detail)
	}
}

func TestCheckPluginReportsADigestMismatch(t *testing.T) {
	t.Setenv(pluginstore.StoreEnvOverride, t.TempDir())
	dir, digest := fakePlugin(t)
	t.Setenv("PATH", dir)

	pinned := "sha256:" + strings.Repeat("0", 64)
	r := &Result{}
	r.checkPlugin(config.Plugin{Command: "letsgo-fake", Version: "v1.0.0", Digest: pinned}, ".", "")

	c := r.Checks[0]
	if c.Status != Fail {
		t.Errorf("Status = %v, want Fail", c.Status)
	}
	if !strings.Contains(c.Detail, "digest mismatch") {
		t.Errorf("Detail = %q, want it to say digest mismatch", c.Detail)
	}
	if !strings.Contains(c.Detail, digest[:19]) {
		t.Errorf("Detail = %q, want it to include the installed digest", c.Detail)
	}
}

func TestRequiresVulncheck(t *testing.T) {
	t.Run("no letsgo.mod", func(t *testing.T) {
		if requiresVulncheck(&config.Config{}) {
			t.Error("want false with no letsgo.mod")
		}
	})

	t.Run("require vulncheck", func(t *testing.T) {
		if !requiresVulncheck(&config.Config{Required: []string{"vulncheck"}}) {
			t.Error("want true")
		}
	})

	t.Run("require something else", func(t *testing.T) {
		if requiresVulncheck(&config.Config{Required: []string{"api-gate"}}) {
			t.Error("want false")
		}
	})
}

func TestLoadConfigTreatsAMissingFileAsAbsentButReportsOtherReadErrors(t *testing.T) {
	cfg, err := loadConfig(t.TempDir())
	if err != nil || cfg == nil {
		t.Fatalf("missing file: cfg = %v, err = %v, want an empty config and no error", cfg, err)
	}

	// A directory where letsgo.mod should be is a read error for every user,
	// root included; it must not be passed off as "no letsgo.mod".
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, config.FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(dir); err == nil {
		t.Fatal("a read error was swallowed as a missing file")
	}
}

// The running letsgo is compared with the record install.sh left beside it:
// no record, no row; a match is OK; a binary that changed since is a warning.
func TestCheckSelfComparesTheInstallRecord(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "letsgo")
	if err := os.WriteFile(bin, []byte("installed bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	original := executable
	t.Cleanup(func() { executable = original })
	executable = func() (string, error) { return bin, nil }

	run := func() []Check {
		r := &Result{}
		r.checkSelf()
		return r.Checks
	}

	if got := run(); len(got) != 0 {
		t.Fatalf("with no record, checks = %+v, want none", got)
	}

	sum := sha256.Sum256([]byte("installed bytes"))
	rec := &install.Record{Schema: 1, Via: "install.sh", Tag: "v1.0.0", Binary: "letsgo", BinarySHA256: hex.EncodeToString(sum[:])}
	if err := install.WriteRecord(bin, rec); err != nil {
		t.Fatal(err)
	}
	if got := run(); len(got) != 1 || got[0].Status != OK || !strings.Contains(got[0].Detail, "v1.0.0 via install.sh") {
		t.Fatalf("with a matching record, checks = %+v", got)
	}

	if err := os.WriteFile(bin, []byte("swapped bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if got := run(); len(got) != 1 || got[0].Status != Warn || !strings.Contains(got[0].Detail, "is not the v1.0.0 binary") {
		t.Fatalf("with a changed binary, checks = %+v", got)
	}
}

// A record that cannot be read is reported rather than ignored, and a
// running binary that cannot be found gets no row at all.
func TestCheckSelfReportsWhatItCannotRead(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "letsgo")
	if err := os.WriteFile(bin, []byte("bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if err := os.WriteFile(install.RecordPath(bin), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := executable
	t.Cleanup(func() { executable = original })

	executable = func() (string, error) { return bin, nil }
	r := &Result{}
	r.checkSelf()
	if len(r.Checks) != 1 || r.Checks[0].Status != Warn || !strings.Contains(r.Checks[0].Detail, "not an install record") {
		t.Errorf("with a garbled record, checks = %+v", r.Checks)
	}

	executable = func() (string, error) { return "", os.ErrNotExist }
	r = &Result{}
	r.checkSelf()
	if len(r.Checks) != 0 {
		t.Errorf("with no executable, checks = %+v, want none", r.Checks)
	}
}
