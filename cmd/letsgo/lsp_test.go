package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

func TestRunLSPRejectsExtraArgs(t *testing.T) {
	err := unwired.runLSP([]string{"extra"})
	if err == nil || !strings.HasPrefix(err.Error(), "usage: ") {
		t.Errorf("unwired.runLSP([extra]) = %v, want a usage error", err)
	}
}

// A client that shuts the server down cleanly ends the process with success,
// which is the whole path from the command line to a running server.
func TestRunLSPServesUntilShutdown(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin; r.Close() })

	frame := func(body string) string {
		return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	go func() {
		defer w.Close()
		fmt.Fprint(w, frame(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}`))
		fmt.Fprint(w, frame(`{"jsonrpc":"2.0","method":"exit"}`))
	}()

	var runErr error
	out := captureStdout(t, func() { runErr = unwired.runLSP([]string{"--restricted"}) })
	if runErr != nil {
		t.Fatalf("runLSP = %v", runErr)
	}
	if !strings.Contains(out, `"id":1`) {
		t.Errorf("no answer to the shutdown request: %q", out)
	}
}

func TestLSPOptionsFollowRestrictedMode(t *testing.T) {
	global := &config.Global{PluginsDir: "/plugins", Go: "/nonexistent/go"}

	open := (forge{global: &config.Global{PluginsDir: "/plugins"}}).lspOptions(false)
	if open.Restricted || open.ResolvePin == nil || open.InstallPin == nil || open.PluginsDir != "/plugins" {
		t.Errorf("unrestricted options = %+v", open)
	}

	closed := (forge{global: global}).lspOptions(true)
	if !closed.Restricted || closed.GoBin != "" || closed.ResolvePin != nil || closed.InstallPin != nil {
		t.Errorf("restricted options = %+v", closed)
	}
}

// A go command the machine config names but that is not there leaves
// build-target completion empty rather than failing the server.
func TestLSPOptionsToleratesAnUnresolvableGo(t *testing.T) {
	opts := (forge{global: &config.Global{Path: "config.mod", Go: "/nonexistent/go"}}).lspOptions(false)
	if opts.GoBin != "" {
		t.Errorf("GoBin = %q, want none", opts.GoBin)
	}
}
