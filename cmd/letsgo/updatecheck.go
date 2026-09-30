package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/updatecheck"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// stderrIsTerminal reports whether a person is reading stderr. A variable so a
// test can be that person.
var stderrIsTerminal = func() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// latestVersion asks the forge for letsgo's newest stable release. A variable
// so a test does not need a network.
//
// Unauthenticated on purpose: a courtesy check should never run a credential
// helper, and one request per interval is well inside the anonymous rate
// limit.
var latestVersion = func(ctx context.Context) (string, error) {
	return selfupdate.Latest(ctx, selfupdate.Options{
		Repo: repository, UserAgent: "letsgo/" + version, APIEndpoint: releaseAPIEndpoint,
	})
}

// releaseAPIEndpoint overrides the forge's API host. Empty means the real one;
// only a test sets it.
var releaseAPIEndpoint string

// maybeNoticeUpdate is what main calls once a command is done. A global config
// that does not load is not worth a message here: the commands that need it
// have already said so.
func maybeNoticeUpdate(command string, args []string) {
	if global, err := config.LoadGlobal(); err == nil {
		printUpdateNotice(os.Stderr, global, command, args)
	}
}

// printUpdateNotice writes at most one line to w, telling the user that a newer
// release exists, when the global config opts in and nothing about this
// invocation makes a stray line unwelcome.
func printUpdateNotice(w io.Writer, global *config.Global, command string, args []string) {
	interval := updatecheck.Interval(global.UpdateCheck)
	if interval == 0 || updatecheck.Suppressed(command, args, os.Getenv, stderrIsTerminal()) {
		return
	}

	checker := updatecheck.Checker{
		Dir:      updatecheck.Dir(global, os.UserCacheDir),
		Interval: interval,
		Current:  version,
		Lookup:   latestVersion,
	}
	if notice := checker.Notice(context.Background()); notice != "" {
		fmt.Fprintln(w, notice)
	}
}
