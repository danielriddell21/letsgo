package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func decodeGlobalString(t *testing.T, body string) (*Global, error) {
	t.Helper()
	f, err := Parse("config.mod", []byte(body))
	if err != nil {
		return nil, err
	}
	return DecodeGlobal(f)
}

func TestDecodeGlobalParsesEveryDirective(t *testing.T) {
	g, err := decodeGlobalString(t, strings.Join([]string{
		"go /opt/go/bin/go",
		"git /usr/bin/git",
		"tool govulncheck /opt/bin/govulncheck",
		"tool apidiff /opt/bin/apidiff",
		"cache /var/cache/letsgo",
		"plugins /var/lib/letsgo/plugins",
		"plugin-repo acme/letsgo-plugins",
		"proxy https://goproxy.acme.internal",
		"token-command gh auth token",
		"color auto",
		"update-check weekly",
	}, "\n")+"\n")
	if err != nil {
		t.Fatalf("DecodeGlobal: %v", err)
	}

	switch {
	case g.Go != "/opt/go/bin/go":
		t.Errorf("Go = %q", g.Go)
	case g.Git != "/usr/bin/git":
		t.Errorf("Git = %q", g.Git)
	case g.Tools["govulncheck"] != "/opt/bin/govulncheck":
		t.Errorf("Tools[govulncheck] = %q", g.Tools["govulncheck"])
	case g.Tools["apidiff"] != "/opt/bin/apidiff":
		t.Errorf("Tools[apidiff] = %q", g.Tools["apidiff"])
	case g.CacheDir != "/var/cache/letsgo":
		t.Errorf("CacheDir = %q", g.CacheDir)
	case g.PluginsDir != "/var/lib/letsgo/plugins":
		t.Errorf("PluginsDir = %q", g.PluginsDir)
	case g.PluginRepo != "acme/letsgo-plugins":
		t.Errorf("PluginRepo = %q", g.PluginRepo)
	case g.Proxy != "https://goproxy.acme.internal":
		t.Errorf("Proxy = %q", g.Proxy)
	case !reflect.DeepEqual(g.TokenCommand, []string{"gh", "auth", "token"}):
		t.Errorf("TokenCommand = %q", g.TokenCommand)
	case g.Color != "auto":
		t.Errorf("Color = %q", g.Color)
	case g.UpdateCheck != "weekly":
		t.Errorf("UpdateCheck = %q", g.UpdateCheck)
	}
}

// token-command takes a whole argv, unlike every other directive's fixed
// arity, so a single word must still parse.
func TestDecodeGlobalTokenCommandAcceptsASingleWord(t *testing.T) {
	g, err := decodeGlobalString(t, "token-command my-helper\n")
	if err != nil {
		t.Fatalf("DecodeGlobal: %v", err)
	}
	if !reflect.DeepEqual(g.TokenCommand, []string{"my-helper"}) {
		t.Errorf("TokenCommand = %q", g.TokenCommand)
	}
}

func TestDecodeGlobalRejectsAnInvalidColor(t *testing.T) {
	if _, err := decodeGlobalString(t, "color purple\n"); err == nil {
		t.Fatal("expected an error for an invalid color")
	}
}

func TestDecodeGlobalRejectsAnInvalidUpdateCheck(t *testing.T) {
	if _, err := decodeGlobalString(t, "update-check hourly\n"); err == nil {
		t.Fatal("expected an error for an invalid update-check value")
	}
}

func TestDecodeGlobalCacheOff(t *testing.T) {
	g, err := decodeGlobalString(t, "cache off\n")
	if err != nil {
		t.Fatalf("DecodeGlobal: %v", err)
	}
	if !g.CacheOff || g.CacheDir != "" {
		t.Errorf("got CacheOff=%v CacheDir=%q, want CacheOff=true CacheDir=\"\"", g.CacheOff, g.CacheDir)
	}
}

// CD-8: a directive that belongs in letsgo.mod must be rejected here rather
// than silently accepted, since nothing global may change what a release
// builds, gates, versions or publishes.
func TestDecodeGlobalRejectsReleaseDirectives(t *testing.T) {
	for _, body := range []string{
		"build linux/amd64\n",
		"project foo\n",
		"version internal/build.Version\n",
		"disable sbom\n",
	} {
		_, err := decodeGlobalString(t, body)
		if err == nil {
			t.Errorf("%q: expected an error", body)
			continue
		}
		if !strings.Contains(err.Error(), "belongs in letsgo.mod") {
			t.Errorf("%q: got %v, want it to say the directive belongs in letsgo.mod", body, err)
		}
	}
}

// The other direction: a global directive pasted into letsgo.mod says where
// it belongs, instead of suggesting the nearest repository directive.
func TestDecodeRejectsGlobalDirectives(t *testing.T) {
	for _, name := range GlobalDirectives() {
		_, err := Decode(parse(t, name+" x\n"))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), name+" belongs in the global config, not letsgo.mod") {
			t.Errorf("%s: got %v, want it to say the directive belongs in the global config", name, err)
		}
	}
}

func TestDecodeGlobalRejectsAnUnknownDirective(t *testing.T) {
	_, err := decodeGlobalString(t, "gti /usr/bin/git\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `did you mean "git"`) {
		t.Errorf("got %v, want a did-you-mean suggestion for git", err)
	}
}

func TestDecodeGlobalRejectsADuplicateScalar(t *testing.T) {
	_, err := decodeGlobalString(t, "proxy https://a\nproxy https://b\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "already set") {
		t.Errorf("got %v", err)
	}
}

func TestDecodeGlobalRejectsADuplicateTool(t *testing.T) {
	_, err := decodeGlobalString(t, "tool govulncheck /a\ntool govulncheck /b\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "already set") {
		t.Errorf("got %v", err)
	}
}

func TestDecodeGlobalRejectsABlock(t *testing.T) {
	_, err := decodeGlobalString(t, "tool (\n  govulncheck /a\n)\n")
	if err == nil {
		t.Fatal("expected an error")
	}
}

// A missing file at the default location is fine: it means the machine has
// no global config, not that something is wrong.
func TestLoadGlobalDefaultsWhenTheFileIsAbsent(t *testing.T) {
	t.Setenv(GlobalConfigEnvOverride, "")

	g, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if g.Go != "" || g.Proxy != "" {
		t.Errorf("expected a zero Global, got %+v", g)
	}
}

func TestLoadGlobalHonoursTheEnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.mod")
	if err := os.WriteFile(path, []byte("proxy https://p.internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(GlobalConfigEnvOverride, path)

	g, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if g.Proxy != "https://p.internal" {
		t.Errorf("Proxy = %q", g.Proxy)
	}
	if g.Path != path {
		t.Errorf("Path = %q, want %q", g.Path, path)
	}
}

func TestLoadGlobalRejectsAMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.mod")
	if err := os.WriteFile(path, []byte("build linux/amd64\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(GlobalConfigEnvOverride, path)

	if _, err := LoadGlobal(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestGlobalPathDefaultsToUserConfigDir(t *testing.T) {
	t.Setenv(GlobalConfigEnvOverride, "")
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config dir on this platform")
	}

	path, explicit, err := GlobalPath()
	if err != nil {
		t.Fatalf("GlobalPath: %v", err)
	}
	if explicit {
		t.Error("explicit = true with no override set")
	}
	want := filepath.Join(configDir, "letsgo", "config.mod")
	if path != want {
		t.Errorf("GlobalPath() = %q, want %q", path, want)
	}
}

// A missing default file is fine (LoadGlobal falls back to defaults), but a
// missing file named explicitly by LETSGO_CONFIG is a mistake worth failing
// on: silently ignoring it would mean a typo'd path is indistinguishable
// from "no config".
func TestLoadGlobalRejectsAMissingOverride(t *testing.T) {
	t.Setenv(GlobalConfigEnvOverride, filepath.Join(t.TempDir(), "does-not-exist.mod"))

	if _, err := LoadGlobal(); err == nil {
		t.Fatal("expected an error")
	}
}

// TestEveryGlobalDirectiveHasADoc holds globalDocs in step with globalKnown,
// the same way TestEveryDirectiveHasADoc holds decode.go's docs in step.
func TestEveryGlobalDirectiveHasADoc(t *testing.T) {
	for name := range globalKnown {
		if globalDocs[name] == "" {
			t.Errorf("global directive %q has no hover doc", name)
		}
	}
	for name := range globalDocs {
		if globalKnown[name] == "" {
			t.Errorf("global doc %q has no matching directive", name)
		}
	}
}

func TestGlobalDocReturnsUsageAndText(t *testing.T) {
	usage, doc, ok := GlobalDoc("go")
	if !ok || usage == "" || doc == "" {
		t.Errorf("GlobalDoc(go) = %q, %q, %v, want non-empty usage and doc", usage, doc, ok)
	}
	if _, _, ok := GlobalDoc("not-a-real-directive"); ok {
		t.Error("GlobalDoc(not-a-real-directive) ok = true, want false")
	}
}

func TestGlobalDirectivesListsEveryKnownDirectiveSorted(t *testing.T) {
	names := GlobalDirectives()
	if len(names) != len(globalKnown) {
		t.Fatalf("len(names) = %d, want %d", len(names), len(globalKnown))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
