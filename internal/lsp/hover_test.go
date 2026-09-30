package lsp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/lsp"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

func TestHoverShowsLiveStateOnlyWhenNotRestricted(t *testing.T) {
	t.Setenv(pluginstore.StoreEnvOverride, t.TempDir())
	t.Setenv("PATH", t.TempDir())

	tests := []struct {
		name string
		opts lsp.Options
		want string // substring; "" means the docs alone
	}{
		{"live", lsp.Options{}, "not installed"},
		{"restricted", lsp.Options{Restricted: true}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, uri := pinEditor(t, tt.opts, "letsgo.mod", pinLine+"\n")
			raw := c.request("textDocument/hover", map[string]any{
				"textDocument": map[string]any{"uri": uri},
				"position":     map[string]any{"line": 0, "character": 2},
			})
			var hover lsp.Hover
			if err := json.Unmarshal(raw, &hover); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(hover.Contents, "Pins an external program") {
				t.Errorf("contents = %q, want the directive docs", hover.Contents)
			}
			if got := strings.Contains(hover.Contents, "not installed"); got != (tt.want != "") {
				t.Errorf("contents = %q, install state shown = %v, want %v", hover.Contents, got, tt.want != "")
			}
		})
	}
}
