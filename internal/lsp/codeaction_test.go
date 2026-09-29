package lsp_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/lsp"
)

const (
	oldDigest = "sha256:" + "0000000000000000000000000000000000000000000000000000000000000000"
	newDigest = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	pinLine   = "plugin ldflags letsgo-env v0.1.0 " + oldDigest
)

// pinEditor opens a letsgo.mod holding text in a fresh server and returns the
// client and the document's URI.
func pinEditor(t *testing.T, opts lsp.Options, name, text string) (*client, string) {
	t.Helper()
	uri := "file://" + filepath.Join(t.TempDir(), name)
	c := newClient(t, opts)
	c.request("initialize", map[string]any{})
	c.notify("initialized", map[string]any{})
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "text": text}})
	c.awaitNotification("textDocument/publishDiagnostics") // the pipe is unbuffered: drain it before the next request
	return c, uri
}

func codeActions(t *testing.T, c *client, uri string) []lsp.CodeAction {
	t.Helper()
	raw := c.request("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range":        map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 1, "character": 0}},
	})
	var actions []lsp.CodeAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatal(err)
	}
	return actions
}

func fixedPin(pin lsp.Pin, err error) lsp.PinResolver {
	return func(context.Context, string) (lsp.Pin, error) { return pin, err }
}

func TestCodeActionUpdatesAPinToTheLatestRelease(t *testing.T) {
	c, uri := pinEditor(t, lsp.Options{ResolvePin: fixedPin(lsp.Pin{Version: "v0.2.0", Digest: newDigest}, nil)}, "letsgo.mod", pinLine+"\n")

	actions := codeActions(t, c, uri)
	if len(actions) != 1 || actions[0].Kind != "quickfix" || actions[0].Edit != nil {
		t.Fatalf("actions = %+v, want one unresolved quickfix", actions)
	}

	body, err := json.Marshal(actions[0])
	if err != nil {
		t.Fatal(err)
	}
	var resolved lsp.CodeAction
	if err := json.Unmarshal(c.request("codeAction/resolve", json.RawMessage(body)), &resolved); err != nil {
		t.Fatal(err)
	}
	edits := resolved.Edit.Changes[uri]
	want := lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: 26}, End: lsp.Position{Line: 0, Character: len(pinLine)}},
		NewText: "v0.2.0 " + newDigest,
	}
	if len(edits) != 1 || edits[0] != want {
		t.Errorf("edits = %+v, want [%+v]", edits, want)
	}
}

func TestCodeActionResolveMakesNoEditWhenThePinIsCurrent(t *testing.T) {
	c, uri := pinEditor(t, lsp.Options{ResolvePin: fixedPin(lsp.Pin{Version: "v0.1.0", Digest: oldDigest}, nil)}, "letsgo.mod", pinLine+"\n")

	body, _ := json.Marshal(codeActions(t, c, uri)[0])
	var resolved lsp.CodeAction
	if err := json.Unmarshal(c.request("codeAction/resolve", json.RawMessage(body)), &resolved); err != nil {
		t.Fatal(err)
	}
	if edits := resolved.Edit.Changes[uri]; len(edits) != 0 {
		t.Errorf("edits = %+v, want none", edits)
	}
}

func TestCodeActionResolveReportsAFailedLookup(t *testing.T) {
	c, uri := pinEditor(t, lsp.Options{ResolvePin: fixedPin(lsp.Pin{}, errors.New("offline"))}, "letsgo.mod", pinLine+"\n")

	body, _ := json.Marshal(codeActions(t, c, uri)[0])
	var resolved lsp.CodeAction
	if err := json.Unmarshal(c.request("codeAction/resolve", json.RawMessage(body)), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Edit != nil {
		t.Errorf("edit = %+v, want none", resolved.Edit)
	}
	var msg struct {
		Type    int    `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(c.awaitNotification("window/showMessage"), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != 2 || msg.Message != "letsgo: could not update letsgo-env: offline" {
		t.Errorf("message = %+v", msg)
	}
}

func TestCodeActionIsOfferedOnlyWhereAPinCanBeUpdated(t *testing.T) {
	resolver := fixedPin(lsp.Pin{Version: "v0.2.0", Digest: newDigest}, nil)
	tests := []struct {
		name, file, text string
		opts             lsp.Options
	}{
		{"restricted", "letsgo.mod", pinLine + "\n", lsp.Options{Restricted: true, ResolvePin: resolver}},
		{"no resolver", "letsgo.mod", pinLine + "\n", lsp.Options{}},
		{"global config", "config.mod", pinLine + "\n", lsp.Options{ResolvePin: resolver}},
		{"no pin on the line", "letsgo.mod", "build linux/amd64\n", lsp.Options{ResolvePin: resolver}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, uri := pinEditor(t, tt.opts, tt.file, tt.text)
			if actions := codeActions(t, c, uri); len(actions) != 0 {
				t.Errorf("actions = %+v, want none", actions)
			}
		})
	}
}

func TestInitializeAdvertisesCodeActionsOnlyWithAResolver(t *testing.T) {
	tests := []struct {
		name string
		opts lsp.Options
		want bool
	}{
		{"resolver", lsp.Options{ResolvePin: fixedPin(lsp.Pin{}, nil)}, true},
		{"restricted", lsp.Options{Restricted: true, ResolvePin: fixedPin(lsp.Pin{}, nil)}, false},
		{"none", lsp.Options{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var init struct {
				Capabilities struct {
					CodeActionProvider *struct {
						ResolveProvider bool `json:"resolveProvider"`
					} `json:"codeActionProvider"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(newClient(t, tt.opts).request("initialize", map[string]any{}), &init); err != nil {
				t.Fatal(err)
			}
			if got := init.Capabilities.CodeActionProvider != nil && init.Capabilities.CodeActionProvider.ResolveProvider; got != tt.want {
				t.Errorf("code actions advertised = %v, want %v", got, tt.want)
			}
		})
	}
}
