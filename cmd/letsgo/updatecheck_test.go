package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

// noticeRig stands in for an interactive terminal, a forge whose newest stable
// release is 1.3.0, and a cache directory of its own.
type noticeRig struct {
	lookups int
	err     error
}

func newNoticeRig(t *testing.T) *noticeRig {
	t.Helper()

	r := &noticeRig{}
	oldVersion, oldTTY, oldLatest := version, stderrIsTerminal, latestVersion
	t.Cleanup(func() { version, stderrIsTerminal, latestVersion = oldVersion, oldTTY, oldLatest })

	version = "1.2.0"
	stderrIsTerminal = func() bool { return true }
	latestVersion = func(context.Context) (string, error) {
		r.lookups++
		return "1.3.0", r.err
	}
	t.Setenv("CI", "")
	return r
}

func notice(t *testing.T, g *config.Global, command string, args ...string) string {
	t.Helper()
	if g.CacheDir == "" && !g.CacheOff {
		g.CacheDir = t.TempDir()
	}
	var out bytes.Buffer
	printUpdateNotice(&out, g, command, args)
	return out.String()
}

func TestUpdateNoticeIsOptIn(t *testing.T) {
	r := newNoticeRig(t)

	for _, setting := range []string{"", "off"} {
		if got := notice(t, &config.Global{UpdateCheck: setting}, "plan"); got != "" || r.lookups != 0 {
			t.Errorf("update-check %q: notice %q after %d lookups", setting, got, r.lookups)
		}
	}
}

func TestUpdateNoticeIsOneLineOnStderr(t *testing.T) {
	newNoticeRig(t)

	for _, setting := range []string{"daily", "weekly"} {
		got := notice(t, &config.Global{UpdateCheck: setting}, "plan", "--explain")
		want := "letsgo v1.3.0 is available (you have v1.2.0): run `letsgo update`\n"
		if got != want {
			t.Errorf("update-check %s: notice %q, want %q", setting, got, want)
		}
	}
}

func TestUpdateNoticeRemembersTheLookup(t *testing.T) {
	r := newNoticeRig(t)
	g := &config.Global{UpdateCheck: "daily", CacheDir: t.TempDir()}

	notice(t, g, "plan")
	notice(t, g, "doctor")
	if r.lookups != 1 {
		t.Errorf("%d lookups across two commands, want 1", r.lookups)
	}
}

func TestUpdateNoticeIsSuppressed(t *testing.T) {
	for name, tc := range map[string]struct {
		setup   func(t *testing.T)
		command string
		args    []string
	}{
		"CI":          {func(t *testing.T) { t.Helper(); t.Setenv("CI", "true") }, "plan", nil},
		"no terminal": {func(*testing.T) { stderrIsTerminal = func() bool { return false } }, "plan", nil},
		"json":        {func(*testing.T) {}, "plan", []string{"--json"}},
		"lsp":         {func(*testing.T) {}, "lsp", nil},
		"update":      {func(*testing.T) {}, "update", nil},
		"version":     {func(*testing.T) {}, "version", nil},
	} {
		r := newNoticeRig(t)
		tc.setup(t)
		if got := notice(t, &config.Global{UpdateCheck: "daily"}, tc.command, tc.args...); got != "" || r.lookups != 0 {
			t.Errorf("%s: notice %q after %d lookups", name, got, r.lookups)
		}
	}
}

func TestUpdateNoticeIsSilentWhenTheLookupFails(t *testing.T) {
	r := newNoticeRig(t)
	r.err = errors.New("offline")

	if got := notice(t, &config.Global{UpdateCheck: "daily"}, "plan"); got != "" {
		t.Errorf("notice = %q, want none", got)
	}
}

// With `cache off` there is nowhere to remember the check, so it is not made.
func TestUpdateNoticeNeedsACache(t *testing.T) {
	r := newNoticeRig(t)

	if got := notice(t, &config.Global{UpdateCheck: "daily", CacheOff: true}, "plan"); got != "" || r.lookups != 0 {
		t.Errorf("notice %q after %d lookups", got, r.lookups)
	}
}

func TestUpdateNoticeHonoursTheUserCacheDir(t *testing.T) {
	r := newNoticeRig(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	printUpdateNotice(&out, &config.Global{UpdateCheck: "daily"}, "plan", nil)
	printUpdateNotice(&out, &config.Global{UpdateCheck: "daily"}, "plan", nil)
	if r.lookups != 1 || strings.Count(out.String(), "letsgo update") != 2 {
		t.Errorf("%d lookups, output %q", r.lookups, out.String())
	}
}

func TestMaybeNoticeUpdateReadsTheGlobalConfig(t *testing.T) {
	newNoticeRig(t)
	t.Setenv("LETSGO_CONFIG", t.TempDir()+"/absent.mod")

	maybeNoticeUpdate(&config.Global{}, "plan", nil) // unset update-check: nothing to print, nothing to fail
}

func TestLatestVersionAsksTheForge(t *testing.T) {
	var path, agent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, agent = r.URL.Path, r.UserAgent()
		_, _ = w.Write([]byte(`[{"tag_name":"v1.4.0"},{"tag_name":"v1.5.0-rc.1"},{"tag_name":"v1.6.0","draft":true}]`))
	}))
	t.Cleanup(server.Close)

	oldEndpoint, oldVersion := releaseAPIEndpoint, version
	t.Cleanup(func() { releaseAPIEndpoint, version = oldEndpoint, oldVersion })
	releaseAPIEndpoint, version = server.URL, "1.2.0"

	got, err := latestVersion(context.Background())
	if err != nil || got != "1.4.0" {
		t.Fatalf("latestVersion = %q, %v; want 1.4.0", got, err)
	}
	if path != "/repos/"+repository+"/releases" || agent != "letsgo/1.2.0" {
		t.Errorf("requested %s as %q", path, agent)
	}
}
