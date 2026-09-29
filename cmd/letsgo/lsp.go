package main

import (
	"context"
	"flag"
	"os"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/lsp"
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

	var goBin string
	if !*restricted {
		// A go binary that can't be resolved is not fatal: build-target
		// completion just comes back empty, the same as it does in
		// restricted mode.
		goBin, _ = gobuild.Toolchain()
	}

	server := lsp.NewServer(os.Stdin, os.Stdout, lsp.Options{Restricted: *restricted, GoBin: goBin})
	code, err := server.Run(context.Background())
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}
