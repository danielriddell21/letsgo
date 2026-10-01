package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// PinInstaller fetches the release a pin names into the plugin store, the way
// `letsgo plugin install` would. Like PinResolver it is injected, so this
// package stays free of the forge and the store's writers.
type PinInstaller func(ctx context.Context, command, version string) error

const (
	// commandInstallPins is the workspace/executeCommand a client sends when
	// one of the install actions is chosen. The server runs it, not the
	// client, so any LSP client can use it without knowing what it does.
	commandInstallPins = "letsgo.installPins"

	messageInfo = 3 // MessageType.Info

	allPins = -1 // installArgs.Line: every pin in the document that needs it
)

// installArgs is the single argument of commandInstallPins: which document,
// and which line of it, or allPins.
type installArgs struct {
	URI  string `json:"uri"`
	Line int    `json:"line"`
}

// pinnable reports whether pins in uri may be looked up at all: not in a
// restricted workspace, and only in a letsgo.mod, the one file that pins.
func (s *Server) pinnable(uri string) bool {
	return !s.opts.Restricted && kindOf(uriToPath(uri)) == kindRepo
}

// needsInstall is true for a pin that cannot run here yet: nothing installed,
// or only a different build on PATH. Installing the pinned release fixes
// either, because the store is consulted before PATH.
func needsInstall(dir, pluginsDir string, pin pinLine) bool {
	state := checkPin(dir, pluginsDir, pin).state
	return state == pinMissing || state == pinMismatch
}

func installAction(title string, args installArgs) CodeAction {
	return CodeAction{
		Title:   title,
		Kind:    codeActionQuickFix,
		Command: &Command{Title: title, Command: commandInstallPins, Arguments: []any{args}},
	}
}

// pinsToInstall lists the pins in the document that need installing: the one
// on line, or every one for allPins. The same release pinned on two hooks is
// listed once.
func pinsToInstall(dir, pluginsDir string, o outline, line int) []pinLine {
	var pins []pinLine
	seen := map[string]bool{}
	for _, n := range slices.Sorted(maps.Keys(o.byLine)) {
		if line != allPins && n != line {
			continue
		}
		pin, ok := o.pin(n)
		key := pin.command + "@" + pin.version
		if !ok || seen[key] || !needsInstall(dir, pluginsDir, pin) {
			continue
		}
		seen[key] = true
		pins = append(pins, pin)
	}
	return pins
}

// handleExecuteCommand runs an install. It reports the outcome in a message,
// for the reason update-pin does: an error in a response is dropped silently
// by most clients, which would read as a click that did nothing.
func (s *Server) handleExecuteCommand(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	if p.Command != commandInstallPins || len(p.Arguments) != 1 {
		return nil, fmt.Errorf("lsp: unsupported command %q", p.Command)
	}
	var args installArgs
	if err := json.Unmarshal(p.Arguments[0], &args); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}

	doc, ok := s.docs[args.URI]
	if !ok || !s.pinnable(args.URI) || s.opts.InstallPin == nil {
		return nil, nil
	}
	dir := filepath.Dir(uriToPath(args.URI))
	pins := pinsToInstall(dir, s.opts.PluginsDir, outlineOf(uriToPath(args.URI), doc.text), args.Line)
	if len(pins) == 0 {
		return nil, s.conn.notify("window/showMessage", showMessageParams{Type: messageInfo, Message: "letsgo: nothing to install"})
	}

	var installed, problems []string
	for _, pin := range pins {
		what := pin.command + " " + pin.version
		if err := s.installOne(ctx, pin); err != nil {
			problems = append(problems, fmt.Sprintf("could not install %s: %v", what, err))
			continue
		}
		if checkPin(dir, s.opts.PluginsDir, pin).state != pinInstalled {
			problems = append(problems, fmt.Sprintf("installed %s, but the release is not the digest the pin names; update the pin", what))
			continue
		}
		installed = append(installed, what)
	}

	var parts []string
	if len(installed) > 0 {
		parts = append(parts, "installed "+strings.Join(installed, ", "))
	}
	parts = append(parts, problems...)
	level := messageInfo
	if len(problems) > 0 {
		level = messageWarning
	}
	return nil, s.conn.notify("window/showMessage", showMessageParams{Type: level, Message: "letsgo: " + strings.Join(parts, "; ")})
}

func (s *Server) installOne(ctx context.Context, pin pinLine) error {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	return s.opts.InstallPin(ctx, pin.command, pin.version)
}
