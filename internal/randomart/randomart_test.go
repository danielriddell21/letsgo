package randomart_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/randomart"
)

// TestMatchesSSHKeygen renders the SHA256 fingerprints of ed25519 keys and
// compares them with what `ssh-keygen -lv` printed for each, captured once
// into testdata. The header and footer are the same ones ssh-keygen draws.
func TestMatchesSSHKeygen(t *testing.T) {
	files, err := filepath.Glob("testdata/k*.txt")
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files: %v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			head, art, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
			_, sum, _ := strings.Cut(strings.Fields(head)[1], ":")
			digest, err := base64.RawStdEncoding.DecodeString(sum)
			if err != nil {
				t.Fatal(err)
			}
			if got := randomart.Render(digest, "ED25519 256"); got != art {
				t.Errorf("art differs from ssh-keygen\n got:\n%s\nwant:\n%s", got, art)
			}
		})
	}
}

// TestEdgeCases pins the shapes a walk can take at its extremes.
func TestEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		digest []byte
	}{
		{"zeros", make([]byte, 32)},
		{"ones", []byte(strings.Repeat("\xff", 32))},
		{"empty", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := randomart.Render(tt.digest, "letsgo v1.0.0")
			lines := strings.Split(got, "\n")
			if len(lines) != 11 {
				t.Fatalf("want 11 lines, got %d", len(lines))
			}
			for _, line := range lines {
				if len(line) != 19 {
					t.Errorf("line %q is %d wide, want 19", line, len(line))
				}
			}
		})
	}
}

func TestEmptyDigestDrawsOnlyTheEndSquare(t *testing.T) {
	field := strings.Join(strings.Split(randomart.Render(nil, "x"), "\n")[1:10], "")
	if strings.Contains(field, "S") || strings.Count(field, "E") != 1 {
		t.Errorf("the start and end share a square and draw as E:\n%s", field)
	}
}

func TestTitle(t *testing.T) {
	tests := []struct{ tag, want string }{
		{"v1.3.0", "letsgo v1.3.0"},
		{"v10.20.30-rc.12", "v10.20.30-rc.12"},
		{"v1.30.0", "letsgo v1.30.0"},
	}
	for _, tt := range tests {
		if got := randomart.Title(tt.tag); got != tt.want {
			t.Errorf("Title(%q) = %q, want %q", tt.tag, got, tt.want)
		}
	}
}

func TestFrameTruncatesALongTitle(t *testing.T) {
	got := randomart.Render(nil, "a-very-long-title-indeed")
	first, _, _ := strings.Cut(got, "\n")
	if len(first) != 19 || !strings.HasPrefix(first, "+[a-very-long-titl") {
		t.Errorf("header %q not truncated to the frame", first)
	}
}
