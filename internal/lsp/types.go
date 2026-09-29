package lsp

import "encoding/json"

// The types below are the minimal subset of the Language Server Protocol
// this server speaks, hand-transcribed from the spec rather than imported —
// the interfaces list in docs/pbs/vscode.md, no more.

// Position is zero-based, opposite of config.Position — the wire format is
// not ours to choose.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Message  string `json:"message"`
}

const (
	SeverityError   = 1
	SeverityWarning = 2
	SeverityInfo    = 3
	SeverityHint    = 4
)

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type TextDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type VersionedTextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type didOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type didCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type textDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type serverCapabilities struct {
	TextDocumentSync           int                `json:"textDocumentSync"`
	CompletionProvider         *struct{}          `json:"completionProvider,omitempty"`
	HoverProvider              bool               `json:"hoverProvider,omitempty"`
	DocumentFormattingProvider bool               `json:"documentFormattingProvider,omitempty"`
	DocumentSymbolProvider     bool               `json:"documentSymbolProvider,omitempty"`
	CodeActionProvider         *codeActionOptions `json:"codeActionProvider,omitempty"`
}

type codeActionOptions struct {
	CodeActionKinds []string `json:"codeActionKinds"`
	ResolveProvider bool     `json:"resolveProvider"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
}

// textDocumentSyncKindFull: the client sends the document's full text on
// every change, rather than incremental edits — simpler, and letsgo.mod is
// never large enough for incremental sync to matter.
const textDocumentSyncKindFull = 1

type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
}

// Completion item kinds this server uses, from the LSP spec's CompletionItemKind.
const (
	CompletionKeyword = 14
	CompletionValue   = 12
	CompletionModule  = 9
)

type Hover struct {
	Contents string `json:"contents"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// Symbol kinds this server uses, from the LSP spec's SymbolKind.
const (
	SymbolKindField     = 8
	SymbolKindNamespace = 3
)

// CodeAction is offered without an edit and resolved into one on demand.
type CodeAction struct {
	Title string          `json:"title"`
	Kind  string          `json:"kind,omitempty"`
	Edit  *WorkspaceEdit  `json:"edit,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
}

type showMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}
