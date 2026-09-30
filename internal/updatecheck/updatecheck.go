// Package updatecheck tells an interactive user, at most once per interval,
// that a newer letsgo release exists.
//
// It is opt-in (the global `update-check` directive) because it is the one
// feature that makes a network request nobody asked for, and it only ever
// mentions an update: nothing is downloaded or installed.
//
// Every dependency that makes it awkward to test — the clock, the cache
// directory, the release lookup — is a field on Checker.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// DefaultTimeout bounds a lookup. A notice is a courtesy, so it must never
// make a command noticeably slower.
const DefaultTimeout = 2 * time.Second

const stateFile = "update-check.json"

// Interval is how often the `update-check` setting allows a lookup; zero
// means never, for "off", for an unset directive and for anything unknown.
func Interval(setting string) time.Duration {
	switch setting {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	}
	return 0
}

// Dir is where the state is kept, or "" when there is nowhere to keep it.
//
// It follows the build cache: the global `cache` directive relocates it, and
// `cache off` disables it, since a check that cannot remember having
// happened would hit the network on every command. Otherwise it is under the
// user cache directory, which userCacheDir resolves (os.UserCacheDir, which
// honours XDG_CACHE_HOME).
func Dir(g *config.Global, userCacheDir func() (string, error)) string {
	switch {
	case g.CacheOff:
		return ""
	case g.CacheDir != "":
		return g.CacheDir
	}
	base, err := userCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "letsgo")
}

// state is what is remembered between invocations.
type state struct {
	// CheckedAt is when a lookup was last attempted, whether or not it
	// succeeded, so that a failing network is not retried on every command.
	CheckedAt time.Time `json:"checked_at"`

	// Latest is the newest stable version a lookup has seen.
	Latest string `json:"latest,omitempty"`
}

// Checker decides whether there is an update worth mentioning.
type Checker struct {
	// Dir holds the state. Empty disables the check.
	Dir string

	// Interval is the minimum time between lookups. Zero disables the check.
	Interval time.Duration

	// Current is the running version.
	Current string

	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	// Lookup returns the newest stable version.
	Lookup func(ctx context.Context) (string, error)

	// Timeout bounds Lookup. Zero means DefaultTimeout.
	Timeout time.Duration
}

// Notice is the line to print, or "" when there is nothing to say. It never
// fails: a lookup that errors or times out is recorded as an attempt and
// otherwise ignored.
func (c Checker) Notice(ctx context.Context) string {
	if c.Dir == "" || c.Interval <= 0 {
		return ""
	}
	// A development build has no version to be behind.
	current, ok := semver.Parse(strings.TrimPrefix(c.Current, "v"))
	if !ok {
		return ""
	}

	now := time.Now
	if c.Now != nil {
		now = c.Now
	}

	st := c.load()
	if c.due(st, now()) {
		st = c.refresh(ctx, st, now())
	}

	latest, ok := semver.Parse(strings.TrimPrefix(st.Latest, "v"))
	if !ok || semver.Compare(latest, current) <= 0 {
		return ""
	}
	return fmt.Sprintf("letsgo v%s is available (you have v%s): run `letsgo update`",
		strings.TrimPrefix(st.Latest, "v"), strings.TrimPrefix(c.Current, "v"))
}

// due reports whether a lookup is owed. A recorded time in the future is a
// clock that moved backwards, and is not trusted to defer one.
func (c Checker) due(st state, now time.Time) bool {
	return st.CheckedAt.IsZero() || st.CheckedAt.After(now) || now.Sub(st.CheckedAt) >= c.Interval
}

// refresh looks up the latest version and records the attempt. A failed lookup
// keeps what was known before.
func (c Checker) refresh(ctx context.Context, st state, now time.Time) state {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	st.CheckedAt = now
	if latest, err := c.Lookup(ctx); err == nil {
		st.Latest = latest
	}
	c.save(st)
	return st
}

func (c Checker) path() string { return filepath.Join(c.Dir, stateFile) }

// load reads the state; a missing or corrupt file is an empty one.
func (c Checker) load() state {
	var st state
	data, err := os.ReadFile(c.path())
	if err != nil || json.Unmarshal(data, &st) != nil {
		return state{}
	}
	return st
}

// save writes the state atomically, and silently: not being able to remember
// is not worth a message.
func (c Checker) save(st state) {
	data, err := json.Marshal(st)
	if err != nil || os.MkdirAll(c.Dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(c.Dir, ".tmp-update-check-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), c.path()) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// quiet lists the commands whose output is for a machine or an editor, or
// that are themselves about versions or updating.
var quiet = []string{"lsp", "update", "version", "--version", "-version", "-v", "help", "-h", "--help"}

// Suppressed reports whether a notice must not be printed for this
// invocation: in CI, when stderr is not a terminal, for a command in quiet,
// and whenever the output is asked to be JSON.
func Suppressed(command string, args []string, getenv func(string) string, stderrIsTerminal bool) bool {
	if !stderrIsTerminal || slices.Contains(quiet, command) {
		return true
	}
	if ci := getenv("CI"); ci != "" && ci != "false" && ci != "0" {
		return true
	}
	return wantsJSON(args)
}

// wantsJSON reports whether args select JSON output: --json (with or without
// a value) or --format json.
func wantsJSON(args []string) bool {
	for i, arg := range args {
		if arg == "--" {
			return false
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if len(arg) < 2 || arg[0] != '-' {
			continue
		}
		switch {
		case name == "json" && (!hasValue || value != "false"):
			return true
		case name == "format" && hasValue && value == "json":
			return true
		case name == "format" && !hasValue && i+1 < len(args) && args[i+1] == "json":
			return true
		}
	}
	return false
}
