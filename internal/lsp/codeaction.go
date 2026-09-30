package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Pin is what an update finds for one plugin: the version and binary digest a
// pin line should now carry.
type Pin struct {
	Version string // as written in letsgo.mod, e.g. "v0.2.0"
	Digest  string // "sha256:<hex>"
}

// PinResolver looks up the latest release of a plugin, installs it into the
// plugin store, and reports the pin that names it. It is injected rather than
// imported so this package stays free of the forge, the store and the network,
// and so a test can stand in for all three.
type PinResolver func(ctx context.Context, command string) (Pin, error)

// resolveTimeout bounds one update. The server serves one request at a time,
// so a lookup that hung would stall diagnostics and completion with it.
const resolveTimeout = 60 * time.Second

const (
	codeActionQuickFix = "quickfix"

	messageWarning = 2 // MessageType.Warning
)

// pinLine is a plugin pin located in one line of text: the byte spans of its
// version and digest fields, which are the only two an update rewrites.
type pinLine struct {
	command       string
	version       string
	digest        string
	start, endCol int // version start and digest end, as byte offsets in the line
}

// pinOnLine reads a single-line `plugin <hook> <command> <version> <digest>`
// pin, or reports ok=false for anything else. It works on the text rather than
// the parsed file because the parser records where a line begins, not where
// each argument does, and an edit needs the latter.
func pinOnLine(line string) (pin pinLine, ok bool) {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}

	type field struct {
		text       string
		start, end int
	}
	var fields []field
	for i := 0; i < len(line); {
		if line[i] == ' ' || line[i] == '\t' {
			i++
			continue
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		fields = append(fields, field{line[start:i], start, i})
	}

	if len(fields) != 5 || fields[0].text != "plugin" {
		return pinLine{}, false
	}
	return pinLine{
		command: fields[2].text,
		version: fields[3].text,
		digest:  fields[4].text,
		start:   fields[3].start,
		endCol:  fields[4].end,
	}, true
}

// codeActionData rides on an unresolved action to the resolve request, which
// is where the network lookup happens: computing the edit in the first
// request would make every lightbulb on a pin line wait on the forge.
type codeActionData struct {
	URI  string `json:"uri"`
	Line int    `json:"line"`
}

func (s *Server) updatable(uri string) bool {
	return !s.opts.Restricted && s.opts.ResolvePin != nil && kindOf(uriToPath(uri)) == kindRepo
}

func (s *Server) handleCodeActionMethod(ctx context.Context, req request) (any, error) {
	if req.Method == "codeAction/resolve" {
		return s.handleCodeActionResolve(ctx, req.Params)
	}
	return s.handleCodeAction(req.Params)
}

func (s *Server) handleCodeAction(raw json.RawMessage) (any, error) {
	var p struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Range        Range                  `json:"range"`
		Context      struct {
			Diagnostics []Diagnostic `json:"diagnostics"`
		} `json:"context"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}

	actions := []CodeAction{}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return actions, nil
	}
	actions = append(actions, didYouMeanActions(p.TextDocument.URI, doc.text, p.Context.Diagnostics)...)
	if !s.updatable(p.TextDocument.URI) {
		return actions, nil
	}

	lines := strings.Split(doc.text, "\n")
	for n := p.Range.Start.Line; n <= p.Range.End.Line && n < len(lines); n++ {
		if n < 0 {
			continue
		}
		pin, ok := pinOnLine(lines[n])
		if !ok {
			continue
		}
		data, err := json.Marshal(codeActionData{URI: p.TextDocument.URI, Line: n})
		if err != nil {
			return nil, fmt.Errorf("lsp: %w", err)
		}
		actions = append(actions, CodeAction{
			Title: fmt.Sprintf("Update pin: %s to the latest release", pin.command),
			Kind:  codeActionQuickFix,
			Data:  data,
		})
	}
	return actions, nil
}

// handleCodeActionResolve fills in the edit. A lookup that fails leaves the
// action without one and says why in a message: a resolve error is dropped
// silently by most clients, which would read as a click that did nothing.
func (s *Server) handleCodeActionResolve(ctx context.Context, raw json.RawMessage) (any, error) {
	var action CodeAction
	if err := json.Unmarshal(raw, &action); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	var data codeActionData
	if err := json.Unmarshal(action.Data, &data); err != nil {
		return action, nil // not one of ours
	}

	doc, ok := s.docs[data.URI]
	if !ok || !s.updatable(data.URI) {
		return action, nil
	}
	lines := strings.Split(doc.text, "\n")
	if data.Line < 0 || data.Line >= len(lines) {
		return action, nil
	}
	pin, ok := pinOnLine(lines[data.Line])
	if !ok {
		return action, nil // the line changed since the action was offered
	}

	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	latest, err := s.opts.ResolvePin(ctx, pin.command)
	if err != nil {
		return action, s.conn.notify("window/showMessage", showMessageParams{
			Type:    messageWarning,
			Message: fmt.Sprintf("letsgo: could not update %s: %v", pin.command, err),
		})
	}

	edits := []TextEdit{}
	if latest.Version != pin.version || latest.Digest != pin.digest {
		edits = append(edits, TextEdit{
			Range: Range{
				Start: Position{Line: data.Line, Character: pin.start},
				End:   Position{Line: data.Line, Character: pin.endCol},
			},
			NewText: latest.Version + " " + latest.Digest,
		})
	}
	action.Edit = &WorkspaceEdit{Changes: map[string][]TextEdit{data.URI: edits}}
	return action, nil
}
