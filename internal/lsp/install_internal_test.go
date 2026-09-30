package lsp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestHandleExecuteCommandRejectsWhatItDoesNotKnow(t *testing.T) {
	tests := []struct {
		name, params string
	}{
		{"malformed", `[`},
		{"other command", `{"command":"letsgo.other","arguments":[{}]}`},
		{"no arguments", `{"command":"letsgo.installPins"}`},
		{"two arguments", `{"command":"letsgo.installPins","arguments":[{},{}]}`},
		{"argument of the wrong shape", `{"command":"letsgo.installPins","arguments":["x"]}`},
	}
	s := NewServer(strings.NewReader(""), io.Discard, Options{InstallPin: func(context.Context, string, string) error { return nil }})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.handleExecuteCommand(t.Context(), json.RawMessage(tt.params)); err == nil {
				t.Error("err = nil, want an error")
			}
		})
	}
}
