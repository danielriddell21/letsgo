package lsp

import "testing"

func TestTokensOf(t *testing.T) {
	tests := []struct {
		name, line string
		want       []token
	}{
		{"words", "build linux/amd64", []token{{"build", 0, 5}, {"linux/amd64", 6, 17}}},
		{"tabs and runs of space", "a \t b", []token{{"a", 0, 1}, {"b", 4, 5}}},
		{"trailing comment", "tags x // why", []token{{"tags", 0, 4}, {"x", 5, 6}}},
		{"comment only", "// nothing", nil},
		{"blank", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokensOf(tt.line)
			if len(got) != len(tt.want) {
				t.Fatalf("tokensOf(%q) = %v, want %v", tt.line, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("tokensOf(%q)[%d] = %v, want %v", tt.line, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTokenAt(t *testing.T) {
	tokens := tokensOf("build linux/amd64")
	tests := []struct {
		character, want int
	}{{0, 0}, {5, 0}, {6, 1}, {17, 1}, {18, -1}}
	for _, tt := range tests {
		if got := tokenAt(tokens, tt.character); got != tt.want {
			t.Errorf("tokenAt(%d) = %d, want %d", tt.character, got, tt.want)
		}
	}
	if got := tokenAt(nil, 0); got != -1 {
		t.Errorf("tokenAt(nil) = %d, want -1", got)
	}
}
