package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestConnRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := newConn(&buf, &buf)

	if err := c.writeResult(json.RawMessage("1"), map[string]string{"ok": "yes"}); err != nil {
		t.Fatal(err)
	}
	if err := c.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: "file:///a"}); err != nil {
		t.Fatal(err)
	}

	first, err := c.readMessage()
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(first, &resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.ID) != "1" {
		t.Errorf("ID = %s, want 1", resp.ID)
	}

	second, err := c.readMessage()
	if err != nil {
		t.Fatal(err)
	}
	var note notification
	if err := json.Unmarshal(second, &note); err != nil {
		t.Fatal(err)
	}
	if note.Method != "textDocument/publishDiagnostics" {
		t.Errorf("Method = %q", note.Method)
	}

	if _, err := c.readMessage(); !errors.Is(err, io.EOF) {
		t.Errorf("readMessage at end = %v, want io.EOF", err)
	}
}

func TestConnReadMessageRejectsMissingContentLength(t *testing.T) {
	c := newConn(bytes.NewBufferString("X-Custom: 1\r\n\r\n"), io.Discard)
	if _, err := c.readMessage(); err == nil {
		t.Fatal("expected an error for a message with no Content-Length")
	}
}

func TestConnReadMessageRejectsAMalformedContentLength(t *testing.T) {
	c := newConn(bytes.NewBufferString("Content-Length: not-a-number\r\n\r\n"), io.Discard)
	if _, err := c.readMessage(); err == nil {
		t.Fatal("expected an error for a non-numeric Content-Length")
	}
}

func TestConnReadMessageRejectsATruncatedBody(t *testing.T) {
	c := newConn(bytes.NewBufferString("Content-Length: 10\r\n\r\n{\"a\":1}"), io.Discard)
	if _, err := c.readMessage(); err == nil {
		t.Fatal("expected an error for a body shorter than Content-Length")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestConnWriteMessagePropagatesAWriteError(t *testing.T) {
	c := newConn(bytes.NewReader(nil), errWriter{})
	if err := c.writeMessage(map[string]string{"ok": "yes"}); err == nil {
		t.Fatal("expected an error when the underlying writer fails")
	}
}

func TestConnWriteErrorCarriesTheCode(t *testing.T) {
	var buf bytes.Buffer
	c := newConn(&buf, &buf)
	if err := c.writeError(json.RawMessage("7"), errInvalidParams, "bad params"); err != nil {
		t.Fatal(err)
	}

	body, err := c.readMessage()
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != errInvalidParams || resp.Error.Message != "bad params" {
		t.Errorf("Error = %+v", resp.Error)
	}
}
