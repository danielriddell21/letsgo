// Package lsp implements letsgo lsp: a language server over stdio for
// letsgo.mod, the global config.mod and .letsgo/*.mod.
//
// It is hand-rolled JSON-RPC rather than an imported client library, the same
// call ADR-0008 makes for the config parser itself: the subset of the
// protocol an editor actually uses is a few hundred lines, and letsgo has no
// third-party dependencies to begin with.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// request is the wire shape of a JSON-RPC request or notification. A
// notification has no ID.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is the wire shape of a JSON-RPC response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// notification is the wire shape of a server-initiated notification, such as
// textDocument/publishDiagnostics.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC / LSP error codes, the only ones this server ever sends.
const (
	errParseError     = -32700
	errMethodNotFound = -32601
	errInvalidParams  = -32602
)

// conn frames JSON-RPC messages over stdio using the LSP "Content-Length"
// header, and serialises writes so a notification never interleaves with a
// response mid-write.
type conn struct {
	r *bufio.Reader
	w io.Writer
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{r: bufio.NewReader(r), w: w}
}

// readMessage reads one framed message, or io.EOF when the stream closes.
func (c *conn) readMessage() ([]byte, error) {
	var length int
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("lsp: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line ends the headers
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length: %w", err)
			}
		}
	}
	if length == 0 {
		return nil, fmt.Errorf("lsp: message with no Content-Length")
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, fmt.Errorf("lsp: %w", err)
	}
	return body, nil
}

func (c *conn) writeMessage(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	return nil
}

func (c *conn) writeResult(id json.RawMessage, result any) error {
	return c.writeMessage(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (c *conn) writeError(id json.RawMessage, code int, message string) error {
	return c.writeMessage(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (c *conn) notify(method string, params any) error {
	return c.writeMessage(notification{JSONRPC: "2.0", Method: method, Params: params})
}
