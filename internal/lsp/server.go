package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Options configure a Server.
type Options struct {
	// Restricted runs in parse-only mode: no plan, no plugin lookup, no
	// subprocess of any kind — what an untrusted workspace gets (ED-11).
	Restricted bool

	// GoBin overrides the go binary used for build-target completion.
	// Empty uses "go" on PATH.
	GoBin string

	// ResolvePin backs the "update pin" code action. Nil, like Restricted,
	// leaves the action out: a server with nothing to look a release up with
	// has nothing honest to offer.
	ResolvePin PinResolver

	// InstallPin backs the "install pinned plugin" code action, under the
	// same conditions as ResolvePin.
	InstallPin PinInstaller
}

type document struct {
	text string
}

// Server is one letsgo lsp session: one stdio connection, one document
// store. It is not safe for concurrent use — the protocol is a single
// request/response stream, and there is nothing here slow enough to be
// worth running two at once.
type Server struct {
	opts         Options
	conn         *conn
	docs         map[string]*document
	shuttingDown bool
}

// NewServer builds a Server reading requests from r and writing responses
// and notifications to w — typically os.Stdin and os.Stdout.
func NewServer(r io.Reader, w io.Writer, opts Options) *Server {
	return &Server{opts: opts, conn: newConn(r, w), docs: map[string]*document{}}
}

// Run serves requests until the client sends "exit" or the connection
// closes. exitCode follows the spec for the exit notification: 0 only when
// a prior "shutdown" request was received, 1 otherwise — a client that
// drops the connection without shutting down first gets treated the same
// as one that sends exit early.
func (s *Server) Run(ctx context.Context) (exitCode int, err error) {
	for {
		body, err := s.conn.readMessage()
		if errors.Is(err, io.EOF) {
			return s.exitCode(), nil
		}
		if err != nil {
			return 1, err
		}

		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			if err := s.conn.writeError(nil, errParseError, err.Error()); err != nil {
				return 1, err
			}
			continue
		}

		if req.Method == "exit" {
			return s.exitCode(), nil
		}

		if err := s.dispatch(ctx, req); err != nil {
			return 1, err
		}
	}
}

func (s *Server) exitCode() int {
	if s.shuttingDown {
		return 0
	}
	return 1
}

func (s *Server) dispatch(ctx context.Context, req request) error {
	isRequest := len(req.ID) > 0

	result, err := s.handle(ctx, req)
	if !isRequest {
		// A notification gets no response even on error — there is no
		// request to fail, only a state update to skip.
		return nil
	}
	if err != nil {
		return s.conn.writeError(req.ID, errInvalidParams, err.Error())
	}
	return s.conn.writeResult(req.ID, result)
}

func (s *Server) handle(ctx context.Context, req request) (any, error) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize()
	case "initialized":
		return nil, nil
	case "shutdown":
		s.shuttingDown = true
		return nil, nil
	case "textDocument/didOpen":
		return nil, s.handleDidOpen(req.Params)
	case "textDocument/didChange":
		return nil, s.handleDidChange(req.Params)
	case "textDocument/didSave":
		return nil, s.handleDidSave(ctx, req.Params)
	case "textDocument/didClose":
		return nil, s.handleDidClose(req.Params)
	case "textDocument/completion":
		return s.handleCompletion(ctx, req.Params)
	case "textDocument/hover":
		return s.handleHover(ctx, req.Params)
	case "textDocument/formatting":
		return s.handleFormatting(req.Params)
	case "textDocument/documentSymbol":
		return s.handleDocumentSymbol(req.Params)
	case "textDocument/codeAction", "codeAction/resolve", "workspace/executeCommand":
		return s.handleCodeActionMethod(ctx, req)
	default:
		if len(req.ID) == 0 {
			return nil, nil // an unknown notification is ignored, not an error
		}
		return nil, fmt.Errorf("lsp: unhandled method %q", req.Method)
	}
}

func (s *Server) handleInitialize() (any, error) {
	caps := serverCapabilities{
		TextDocumentSync:           textDocumentSyncKindFull,
		CompletionProvider:         &struct{}{},
		HoverProvider:              true,
		DocumentFormattingProvider: true,
		DocumentSymbolProvider:     true,
	}
	// Did-you-mean fixes need no lookup, so code actions are always on; only
	// the update-pin action resolves lazily, and only with a resolver.
	caps.CodeActionProvider = &codeActionOptions{
		CodeActionKinds: []string{codeActionQuickFix},
		ResolveProvider: !s.opts.Restricted && s.opts.ResolvePin != nil,
	}
	if !s.opts.Restricted && s.opts.InstallPin != nil {
		caps.ExecuteCommandProvider = &executeCommandOptions{Commands: []string{commandInstallPins}}
	}
	return initializeResult{Capabilities: caps}, nil
}

func (s *Server) handleDidOpen(raw json.RawMessage) error {
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	s.docs[p.TextDocument.URI] = &document{text: p.TextDocument.Text}
	return s.publishDiagnostics(p.TextDocument.URI)
}

func (s *Server) handleDidChange(raw json.RawMessage) error {
	var p didChangeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	if len(p.ContentChanges) == 0 {
		return nil
	}
	// Full sync only (see textDocumentSyncKindFull): the last change is the
	// whole new text.
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		doc = &document{}
		s.docs[p.TextDocument.URI] = doc
	}
	doc.text = text
	return s.publishDiagnostics(p.TextDocument.URI)
}

func (s *Server) handleDidSave(ctx context.Context, raw json.RawMessage) error {
	var p didSaveParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	return s.publishDiagnosticsWithPlan(ctx, p.TextDocument.URI)
}

func (s *Server) handleDidClose(raw json.RawMessage) error {
	var p didCloseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	delete(s.docs, p.TextDocument.URI)
	return nil
}

// publishDiagnostics reports parse/decode diagnostics only — cheap enough to
// run on every keystroke (ED-1).
func (s *Server) publishDiagnostics(uri string) error {
	doc, ok := s.docs[uri]
	if !ok {
		return nil
	}
	diags := parseDiagnostics(uriToPath(uri), doc.text)
	return s.conn.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: uri, Diagnostics: diags})
}

// publishDiagnosticsWithPlan additionally runs a fast local plan when the
// file parses and decodes cleanly and the server is not restricted, adding
// every position-carrying Fail as a diagnostic (ED-2's on-save half).
func (s *Server) publishDiagnosticsWithPlan(ctx context.Context, uri string) error {
	doc, ok := s.docs[uri]
	if !ok {
		return nil
	}
	path := uriToPath(uri)
	diags := parseDiagnostics(path, doc.text)

	if len(diags) == 0 && !s.opts.Restricted && kindOf(path) == kindRepo {
		diags = append(diags, planDiagnostics(ctx, path)...)
	}
	return s.conn.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: uri, Diagnostics: diags})
}

func (s *Server) handleCompletion(ctx context.Context, raw json.RawMessage) (any, error) {
	var p textDocumentPositionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []CompletionItem{}, nil
	}
	path := uriToPath(p.TextDocument.URI)
	items := completions(ctx, path, doc.text, p.Position.Line, p.Position.Character, !s.opts.Restricted, s.opts.GoBin)
	if items == nil {
		items = []CompletionItem{}
	}
	return items, nil
}

func (s *Server) handleHover(ctx context.Context, raw json.RawMessage) (any, error) {
	var p textDocumentPositionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	contents, ok := hoverAt(ctx, uriToPath(p.TextDocument.URI), doc.text, p.Position, !s.opts.Restricted, s.opts.GoBin)
	if !ok {
		return nil, nil
	}
	return Hover{Contents: contents}, nil
}

func (s *Server) handleFormatting(raw json.RawMessage) (any, error) {
	var p struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []TextEdit{}, nil
	}
	edits := formatEdits(uriToPath(p.TextDocument.URI), doc.text)
	if edits == nil {
		edits = []TextEdit{}
	}
	return edits, nil
}

func (s *Server) handleDocumentSymbol(raw json.RawMessage) (any, error) {
	var p struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []DocumentSymbol{}, nil
	}
	symbols := documentSymbols(uriToPath(p.TextDocument.URI), doc.text)
	if symbols == nil {
		symbols = []DocumentSymbol{}
	}
	return symbols, nil
}
