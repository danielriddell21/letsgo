package lsp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/lsp"
)

// client drives a Server over an in-memory pipe the way a real editor drives
// it over stdio: framed JSON-RPC requests out, framed responses and
// notifications in.
type client struct {
	t       *testing.T
	fromSrv *bufReader
	toSrv   io.Writer
	nextID  int
	pending [][]byte // notifications read early while awaiting a response
}

type bufReader struct{ r io.Reader }

func newClient(t *testing.T, opts lsp.Options) *client {
	t.Helper()
	toSrv, toSrvW := io.Pipe()
	fromSrvR, fromSrv := io.Pipe()

	srv := lsp.NewServer(toSrv, fromSrv, opts)
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(t.Context())
	}()
	t.Cleanup(func() {
		toSrvW.Close()
		<-done
	})

	return &client{t: t, fromSrv: &bufReader{r: fromSrvR}, toSrv: toSrvW}
}

func (c *client) request(method string, params any) json.RawMessage {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return c.awaitResponse(id)
}

func (c *client) notify(method string, params any) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *client) send(v any) {
	c.t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.toSrv, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		c.t.Fatal(err)
	}
}

// awaitResponse reads messages until it finds the response matching id,
// stashing any notification seen along the way for awaitNotification.
func (c *client) awaitResponse(id int) json.RawMessage {
	c.t.Helper()
	for {
		body := c.readMessage()
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			c.t.Fatal(err)
		}
		if msg.Method != "" {
			c.pending = append(c.pending, body)
			continue
		}
		gotID, err := strconv.Atoi(string(msg.ID))
		if err != nil {
			c.t.Fatal(err)
		}
		if gotID != id {
			c.t.Fatalf("response id = %d, want %d", gotID, id)
		}
		if msg.Error != nil {
			c.t.Fatalf("response error: %d %s", msg.Error.Code, msg.Error.Message)
		}
		return msg.Result
	}
}

// awaitNotification returns the next notification with the given method,
// checking any already-buffered messages first.
func (c *client) awaitNotification(method string) json.RawMessage {
	c.t.Helper()
	for i, body := range c.pending {
		var msg struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			c.t.Fatal(err)
		}
		if msg.Method == method {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			return msg.Params
		}
	}
	for {
		body := c.readMessage()
		var msg struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			c.t.Fatal(err)
		}
		if msg.Method == method {
			return msg.Params
		}
		c.pending = append(c.pending, body)
	}
}

func (c *client) readMessage() []byte {
	c.t.Helper()
	var length int
	for {
		line := c.readLine()
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				c.t.Fatal(err)
			}
			length = n
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.fromSrv.r, body); err != nil {
		c.t.Fatal(err)
	}
	return body
}

func (c *client) readLine() string {
	c.t.Helper()
	var b []byte
	for {
		buf := make([]byte, 1)
		if _, err := c.fromSrv.r.Read(buf); err != nil {
			c.t.Fatal(err)
		}
		b = append(b, buf[0])
		if buf[0] == '\n' {
			return string(b)
		}
	}
}

func TestServerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeModuleFixture(t, dir)
	path := filepath.Join(dir, "letsgo.mod")
	uri := "file://" + path

	c := newClient(t, lsp.Options{})

	initResult := c.request("initialize", map[string]any{})
	var init struct {
		Capabilities struct {
			HoverProvider              bool `json:"hoverProvider"`
			DocumentFormattingProvider bool `json:"documentFormattingProvider"`
			DocumentSymbolProvider     bool `json:"documentSymbolProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initResult, &init); err != nil {
		t.Fatal(err)
	}
	if !init.Capabilities.HoverProvider || !init.Capabilities.DocumentFormattingProvider || !init.Capabilities.DocumentSymbolProvider {
		t.Fatalf("capabilities = %+v, want all true", init.Capabilities)
	}
	c.notify("initialized", map[string]any{})

	text := "build linux/amd64\n"
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": text},
	})
	diagParams := c.awaitNotification("textDocument/publishDiagnostics")
	var diags struct {
		Diagnostics []lsp.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(diagParams, &diags); err != nil {
		t.Fatal(err)
	}
	if len(diags.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none for valid letsgo.mod", diags.Diagnostics)
	}

	hoverResult := c.request("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": 1},
	})
	var hover lsp.Hover
	if err := json.Unmarshal(hoverResult, &hover); err != nil {
		t.Fatal(err)
	}
	if hover.Contents == "" {
		t.Error("hover Contents is empty")
	}

	fmtResult := c.request("textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	var edits []lsp.TextEdit
	if err := json.Unmarshal(fmtResult, &edits); err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 {
		t.Errorf("edits = %+v, want none: the fixture is already formatted", edits)
	}

	symResult := c.request("textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	var symbols []lsp.DocumentSymbol
	if err := json.Unmarshal(symResult, &symbols); err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 || symbols[0].Name != "build linux/amd64" {
		t.Errorf("symbols = %+v, want one \"build linux/amd64\" symbol", symbols)
	}

	compResult := c.request("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": 0},
	})
	var completions []lsp.CompletionItem
	if err := json.Unmarshal(compResult, &completions); err != nil {
		t.Fatal(err)
	}
	if len(completions) == 0 {
		t.Error("completions is empty, want at least the directive list")
	}

	shutdownResult := c.request("shutdown", nil)
	if got := strings.TrimSpace(string(shutdownResult)); got != "" && got != "null" {
		t.Errorf("shutdown result = %q, want empty or null", got)
	}
	c.notify("exit", nil)
}

func TestServerRestrictedModeSkipsPlanDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeModuleFixtureWithBudgetFailure(t, dir)
	path := filepath.Join(dir, "letsgo.mod")
	uri := "file://" + path

	c := newClient(t, lsp.Options{Restricted: true})
	c.request("initialize", map[string]any{})
	c.notify("initialized", map[string]any{})

	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": string(text)},
	})
	c.awaitNotification("textDocument/publishDiagnostics") // didOpen's own parse-only diagnostics

	c.notify("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	saveParams := c.awaitNotification("textDocument/publishDiagnostics")
	var diags struct {
		Diagnostics []lsp.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(saveParams, &diags); err != nil {
		t.Fatal(err)
	}
	if len(diags.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none: restricted mode must not run a plan", diags.Diagnostics)
	}
}

func TestServerCompletionOnAnUnopenedDocumentReturnsEmpty(t *testing.T) {
	c := newClient(t, lsp.Options{})
	c.request("initialize", map[string]any{})
	c.notify("initialized", map[string]any{})

	result := c.request("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": "file:///never-opened.mod"},
		"position":     map[string]any{"line": 0, "character": 0},
	})
	var items []lsp.CompletionItem
	if err := json.Unmarshal(result, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("items = %+v, want none", items)
	}
}

func TestServerShutdownWithoutExitStillEndsTheStream(t *testing.T) {
	c := newClient(t, lsp.Options{})
	c.request("initialize", map[string]any{})
	c.notify("initialized", map[string]any{})
	c.request("shutdown", nil)
	// No exit notification sent — closing the pipe (t.Cleanup) must still
	// end the server's Run loop rather than hang.
}

// writeModuleFixture writes a minimal buildable module with a valid
// letsgo.mod, for tests that need a real git repo plan.Resolve can run
// against.
func writeModuleFixture(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module github.com/you/foo\n\ngo 1.24\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(dir, "letsgo.mod"), "build linux/amd64\n")
}

func writeModuleFixtureWithBudgetFailure(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module github.com/you/foo\n\ngo 1.24\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(dir, "letsgo.mod"), "build linux/amd64\nbudget windows/arm64 10MB\n")
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "user.name=Test", "-c", "user.email=t@example.com", "commit", "-q", "-m", "commit")
	gitRun(t, dir, "tag", "v1.0.0")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
