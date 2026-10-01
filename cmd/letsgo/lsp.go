package main

import (
	"context"
	"flag"
	"os"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/lsp"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// runLSP serves letsgo.mod, the global config.mod and .letsgo/*.mod over
// stdio JSON-RPC: see docs/hld/vscode.md's "letsgo lsp" section. It never
// prints to stdout itself — every byte there is a framed protocol message —
// so a plain terminal invocation just waits for a client that speaks LSP.
func runLSP(args []string) error {
	fs := flag.NewFlagSet("lsp", flag.ExitOnError)
	restricted := fs.Bool("restricted", false, "parse, decode, format and complete only — no plan, no plugin lookup, no subprocess")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage("letsgo lsp [--restricted]")
	}

	server := lsp.NewServer(os.Stdin, os.Stdout, lspOptions(*restricted, machineConfig()))
	code, err := server.Run(context.Background())
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// lspOptions is what the server is wired with: nothing that reaches outside
// the document in restricted mode, and the machine's go command and plugin
// store otherwise.
func lspOptions(restricted bool, global *config.Global) lsp.Options {
	opts := lsp.Options{Restricted: restricted, PluginsDir: global.PluginsDir}
	if restricted {
		return opts
	}
	opts.ResolvePin, opts.InstallPin = latestPin, installPinned
	// A go binary that can't be resolved is not fatal: build-target
	// completion just comes back empty, the same as it does in restricted
	// mode.
	opts.GoBin, _, _ = gobuild.Toolchain(global)
	return opts
}

// latestPin backs the editor's "update pin" action: it installs the newest
// release of a plugin into the store, exactly as `letsgo plugin install` would,
// and reports the pin that names it.
func latestPin(ctx context.Context, command string) (lsp.Pin, error) {
	token, _ := plan.Token(ctx, "")
	release, _, _, err := fetchIntoStore(ctx, selfupdate.Options{
		Repo:      defaultPluginRepo(),
		Token:     token,
		UserAgent: "letsgo/" + version,
		Binary:    command,
	})
	if err != nil {
		return lsp.Pin{}, err
	}
	return lsp.Pin{Version: "v" + release.Version, Digest: "sha256:" + release.BinarySHA256}, nil
}

// installPinned backs the editor's "install pinned plugin" action: it fetches
// the release a pin names into the store, exactly as `letsgo plugin install`
// does for each pin. Whether what it fetched is the digest the pin names is
// for the caller to check — the store is content-addressed, so a release that
// differs simply lands under a different digest.
func installPinned(ctx context.Context, command, tag string) error {
	token, _ := plan.Token(ctx, "")
	_, _, _, err := fetchIntoStore(ctx, selfupdate.Options{
		Repo:      defaultPluginRepo(),
		Token:     token,
		UserAgent: "letsgo/" + version,
		Binary:    command,
		Tag:       tag,
	})
	return err
}
