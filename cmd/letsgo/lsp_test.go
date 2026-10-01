package main

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

func TestRunLSPRejectsExtraArgs(t *testing.T) {
	err := runLSP([]string{"extra"})
	if err == nil || !strings.HasPrefix(err.Error(), "usage: ") {
		t.Errorf("runLSP([extra]) = %v, want a usage error", err)
	}
}

func TestLSPOptionsFollowRestrictedMode(t *testing.T) {
	global := &config.Global{PluginsDir: "/plugins", Go: "/nonexistent/go"}

	open := lspOptions(false, &config.Global{PluginsDir: "/plugins"})
	if open.Restricted || open.ResolvePin == nil || open.InstallPin == nil || open.PluginsDir != "/plugins" {
		t.Errorf("unrestricted options = %+v", open)
	}

	closed := lspOptions(true, global)
	if !closed.Restricted || closed.GoBin != "" || closed.ResolvePin != nil || closed.InstallPin != nil {
		t.Errorf("restricted options = %+v", closed)
	}
}

// A go command the machine config names but that is not there leaves
// build-target completion empty rather than failing the server.
func TestLSPOptionsToleratesAnUnresolvableGo(t *testing.T) {
	opts := lspOptions(false, &config.Global{Path: "config.mod", Go: "/nonexistent/go"})
	if opts.GoBin != "" {
		t.Errorf("GoBin = %q, want none", opts.GoBin)
	}
}
