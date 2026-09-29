package lsp

import "testing"

func TestPinOnLine(t *testing.T) {
	const digest = "sha256:abc"
	tests := []struct {
		name, line, command string
		ok                  bool
		start, end          int
	}{
		{"pin", "plugin ldflags letsgo-env v0.1.0 " + digest, "letsgo-env", true, 26, 43},
		{"tabs and comment", "plugin\tldflags\tletsgo-env\tv0.1.0\t" + digest + " // pinned", "letsgo-env", true, 26, 43},
		{"no pin yet", "plugin ldflags letsgo-env", "", false, 0, 0},
		{"other directive", "build linux/amd64", "", false, 0, 0},
		{"commented out", "// plugin ldflags letsgo-env v0.1.0 " + digest, "", false, 0, 0},
		{"blank", "", "", false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pin, ok := pinOnLine(tt.line)
			if ok != tt.ok || pin.command != tt.command || pin.start != tt.start || pin.endCol != tt.end {
				t.Errorf("pinOnLine(%q) = %+v, %v; want command %q, span %d-%d, ok %v", tt.line, pin, ok, tt.command, tt.start, tt.end, tt.ok)
			}
		})
	}
}
