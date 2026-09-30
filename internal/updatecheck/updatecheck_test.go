package updatecheck_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/updatecheck"
)

var epoch = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

// rig is a Checker whose clock, directory and lookup a test controls.
type rig struct {
	checker updatecheck.Checker
	now     time.Time
	calls   int
	latest  string
	err     error
}

func newRig(t *testing.T, current string) *rig {
	t.Helper()
	r := &rig{now: epoch, latest: "1.3.0"}
	r.checker = updatecheck.Checker{
		Dir:      filepath.Join(t.TempDir(), "letsgo"),
		Interval: 24 * time.Hour,
		Current:  current,
		Now:      func() time.Time { return r.now },
		Lookup: func(context.Context) (string, error) {
			r.calls++
			return r.latest, r.err
		},
	}
	return r
}

func (r *rig) notice() string { return r.checker.Notice(context.Background()) }

func TestNoticeNamesTheNewerRelease(t *testing.T) {
	for _, current := range []string{"1.2.0", "v1.2.0"} {
		r := newRig(t, current)
		want := "letsgo v1.3.0 is available (you have v1.2.0): run `letsgo update`"
		if got := r.notice(); got != want {
			t.Errorf("Notice with current %q = %q, want %q", current, got, want)
		}
	}
}

func TestNoticeIsEmptyWhenThereIsNothingNewer(t *testing.T) {
	for _, current := range []string{"1.3.0", "v1.4.0", "dev", ""} {
		r := newRig(t, current)
		if got := r.notice(); got != "" {
			t.Errorf("Notice with current %q = %q, want none", current, got)
		}
	}
}

func TestNoticeIsDisabledWithoutAnIntervalOrADirectory(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.checker.Interval = 0
	if got := r.notice(); got != "" || r.calls != 0 {
		t.Errorf("no interval: notice %q after %d lookups", got, r.calls)
	}

	r = newRig(t, "1.2.0")
	r.checker.Dir = ""
	if got := r.notice(); got != "" || r.calls != 0 {
		t.Errorf("no directory: notice %q after %d lookups", got, r.calls)
	}
}

// At most one request per interval, however many commands are run.
func TestNoticeLooksUpOncePerInterval(t *testing.T) {
	r := newRig(t, "1.2.0")

	steps := []struct {
		advance time.Duration
		calls   int
	}{
		{0, 1},
		{time.Hour, 1},
		{23 * time.Hour, 2}, // a full day since the first lookup
		{time.Minute, 2},
		{24 * time.Hour, 3},
	}
	for i, s := range steps {
		r.now = r.now.Add(s.advance)
		if got := r.notice(); got == "" {
			t.Errorf("step %d: no notice, the cached version should still be reported", i)
		}
		if r.calls != s.calls {
			t.Errorf("step %d: %d lookups, want %d", i, r.calls, s.calls)
		}
	}
}

func TestNoticeUsesTheNewVersionAfterTheIntervalPasses(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.notice()

	r.latest = "1.5.0"
	r.now = r.now.Add(25 * time.Hour)
	if got := r.notice(); !strings.Contains(got, "v1.5.0") {
		t.Errorf("Notice = %q, want v1.5.0", got)
	}
}

// A clock that moved backwards must not defer the check indefinitely.
func TestNoticeLooksUpAgainWhenTheClockWentBackwards(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.notice()

	r.now = r.now.Add(-48 * time.Hour)
	r.notice()
	if r.calls != 2 {
		t.Errorf("%d lookups, want 2", r.calls)
	}
}

// A failed lookup is silent, is remembered as an attempt, and keeps whatever
// was known before it.
func TestNoticeIsSilentAboutAFailedLookupAndDoesNotRetry(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.err = errors.New("offline")

	for i := range 3 {
		if got := r.notice(); got != "" {
			t.Fatalf("notice %d = %q, want none", i, got)
		}
	}
	if r.calls != 1 {
		t.Errorf("%d lookups, want the failure to be remembered after 1", r.calls)
	}

	r.err = nil
	r.now = r.now.Add(25 * time.Hour)
	if got := r.notice(); got == "" || r.calls != 2 {
		t.Errorf("after the interval: notice %q, %d lookups", got, r.calls)
	}

	r.err = errors.New("offline again")
	r.now = r.now.Add(25 * time.Hour)
	if got := r.notice(); !strings.Contains(got, "v1.3.0") {
		t.Errorf("a failed refresh lost the known version: %q", got)
	}
}

func TestNoticeBoundsTheLookup(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.checker.Timeout = 10 * time.Millisecond
	r.checker.Lookup = func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	start := time.Now()
	if got := r.notice(); got != "" {
		t.Errorf("notice = %q, want none", got)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the lookup was not bounded")
	}
}

func TestNoticeDefaultsTheTimeoutAndClock(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.checker.Now = nil
	r.checker.Timeout = 0
	var deadline time.Time
	r.checker.Lookup = func(ctx context.Context) (string, error) {
		deadline, _ = ctx.Deadline()
		return "1.3.0", nil
	}

	if got := r.notice(); got == "" {
		t.Error("no notice")
	}
	if left := time.Until(deadline); left <= 0 || left > updatecheck.DefaultTimeout {
		t.Errorf("deadline in %v, want within %v", left, updatecheck.DefaultTimeout)
	}
}

func TestStateIsPersistedAcrossCheckers(t *testing.T) {
	r := newRig(t, "1.2.0")
	r.notice()

	data, err := os.ReadFile(filepath.Join(r.checker.Dir, "update-check.json"))
	if err != nil || !strings.Contains(string(data), `"latest":"1.3.0"`) {
		t.Fatalf("state file = %q, %v", data, err)
	}

	again := r.checker
	again.Lookup = func(context.Context) (string, error) {
		t.Error("a second process looked up within the interval")
		return "", nil
	}
	if got := again.Notice(context.Background()); got == "" {
		t.Error("the cached version was not reported")
	}
}

func TestACorruptStateFileIsTreatedAsEmpty(t *testing.T) {
	r := newRig(t, "1.2.0")
	if err := os.MkdirAll(r.checker.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.checker.Dir, "update-check.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := r.notice(); got == "" || r.calls != 1 {
		t.Errorf("notice %q after %d lookups", got, r.calls)
	}
}

// Not being able to remember is not worth a message.
func TestAnUnwritableStateDirectoryIsSilent(t *testing.T) {
	r := newRig(t, "1.2.0")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.checker.Dir = filepath.Join(blocker, "sub")

	if got := r.notice(); got == "" {
		t.Error("the lookup result was dropped because it could not be saved")
	}
}

func TestIntervalMapsTheSetting(t *testing.T) {
	for setting, want := range map[string]time.Duration{
		"daily": 24 * time.Hour, "weekly": 7 * 24 * time.Hour, "off": 0, "": 0, "hourly": 0,
	} {
		if got := updatecheck.Interval(setting); got != want {
			t.Errorf("Interval(%q) = %v, want %v", setting, got, want)
		}
	}
}

func TestDirFollowsTheBuildCache(t *testing.T) {
	ok := func() (string, error) { return "/home/u/.cache", nil }
	broken := func() (string, error) { return "", errors.New("no home") }

	for name, tc := range map[string]struct {
		global *config.Global
		cache  func() (string, error)
		want   string
	}{
		"default":        {&config.Global{}, ok, filepath.Join("/home/u/.cache", "letsgo")},
		"cache dir":      {&config.Global{CacheDir: "/mnt/big"}, ok, "/mnt/big"},
		"cache off":      {&config.Global{CacheOff: true, CacheDir: "/mnt/big"}, ok, ""},
		"no cache dir":   {&config.Global{}, broken, ""},
		"cache dir wins": {&config.Global{CacheDir: "/mnt/big"}, broken, "/mnt/big"},
	} {
		if got := updatecheck.Dir(tc.global, tc.cache); got != tc.want {
			t.Errorf("%s: Dir = %q, want %q", name, got, tc.want)
		}
	}
}

func TestSuppressed(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}

	for name, tc := range map[string]struct {
		command string
		args    []string
		env     func(string) string
		tty     bool
		want    bool
	}{
		"interactive":        {"plan", []string{"--explain"}, env(), true, false},
		"no terminal":        {"plan", nil, env(), false, true},
		"CI":                 {"plan", nil, env("CI", "true"), true, true},
		"CI false":           {"plan", nil, env("CI", "false"), true, false},
		"CI zero":            {"plan", nil, env("CI", "0"), true, false},
		"--json":             {"plan", []string{"--json"}, env(), true, true},
		"-json":              {"doctor", []string{"-json"}, env(), true, true},
		"--json=true":        {"doctor", []string{"--json=true"}, env(), true, true},
		"--json=false":       {"doctor", []string{"--json=false"}, env(), true, false},
		"json after operand": {"verify", []string{"v1.0.0", "--json"}, env(), true, true},
		"format json":        {"diff", []string{"--format", "json"}, env(), true, true},
		"format=json":        {"diff", []string{"--format=json"}, env(), true, true},
		"format md":          {"diff", []string{"--format", "md"}, env(), true, false},
		"format at the end":  {"diff", []string{"--format"}, env(), true, false},
		"after terminator":   {"yank", []string{"--", "--json"}, env(), true, false},
		"a bare word":        {"tag", []string{"json"}, env(), true, false},
		"a lone dash":        {"tag", []string{"-"}, env(), true, false},
		"lsp":                {"lsp", nil, env(), true, true},
		"update":             {"update", []string{"--check"}, env(), true, true},
		"version":            {"version", nil, env(), true, true},
		"--version":          {"--version", nil, env(), true, true},
		"-v":                 {"-v", nil, env(), true, true},
		"help":               {"help", nil, env(), true, true},
		"--help":             {"--help", nil, env(), true, true},
	} {
		if got := updatecheck.Suppressed(tc.command, tc.args, tc.env, tc.tty); got != tc.want {
			t.Errorf("%s: Suppressed = %v, want %v", name, got, tc.want)
		}
	}
}
