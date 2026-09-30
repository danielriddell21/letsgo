package code128_test

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/danielriddell21/letsgo/internal/receipt/code128"
)

func bits(modules []bool) string {
	var b strings.Builder
	for _, m := range modules {
		if m {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// The expected bars were produced by an independent Code 128 implementation.
func TestEncodeMatchesKnownSymbols(t *testing.T) {
	tests := []struct {
		text, bars string
	}{
		{"Hello", "110100100001100010100010110010000110010100001100101000010001111010110010100001100011101011"},
		{"a1b2", "1101001000010010110000100111001101001000011011001110010110010000101100011101011"},
		{"e58a", "1101001000010110010000110111001001110100110010010110000110001101101100011101011"},
	}
	for _, tt := range tests {
		symbols, err := code128.Encode(tt.text)
		if err != nil {
			t.Fatal(err)
		}
		if got := bits(code128.Modules(symbols)); got != tt.bars {
			t.Errorf("%q:\n got %s\nwant %s", tt.text, got, tt.bars)
		}
	}
}

func TestEncodeFramesTheSymbolsWithStartCheckAndStop(t *testing.T) {
	symbols, err := code128.Encode("Hello")
	if err != nil {
		t.Fatal(err)
	}

	// Start B, H e l l o as value = byte - 32, the check character, then stop.
	// Check: (104 + 40*1 + 69*2 + 76*3 + 76*4 + 79*5) mod 103 = 76.
	want := []int{104, 40, 69, 76, 76, 79, 76, 106}
	if !slices.Equal(symbols, want) {
		t.Errorf("symbols = %v, want %v", symbols, want)
	}
}

func TestEncodeRejectsWhatSetBCannotHold(t *testing.T) {
	for _, text := range []string{"tab\there", "café", "\x7f"} {
		if _, err := code128.Encode(text); err == nil {
			t.Errorf("Encode(%q) accepted a character outside printable ASCII", text)
		}
	}
}

func TestEverySymbolIsElevenModulesWide(t *testing.T) {
	for value := 0; value <= 105; value++ {
		if n := len(code128.Modules([]int{value})) - 2; n != 11 {
			t.Errorf("symbol %d is %d modules wide, want 11", value, n)
		}
	}
	// The stop symbol is thirteen modules including its terminating bar.
	if n := len(code128.Modules([]int{106})); n != 13 {
		t.Errorf("stop is %d modules wide, want 13", n)
	}
}

func TestRenderDrawsEachPairOfModulesAsOneCell(t *testing.T) {
	got, err := code128.Render("e58a", 3)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[0] != lines[1] || lines[1] != lines[2] {
		t.Fatalf("want three identical rows, got %q", lines)
	}
	// 1 start + 4 data + 1 check symbols of 11 modules, and a 13 module stop.
	if n := utf8.RuneCountInString(lines[0]); n != 40 {
		t.Errorf("row is %d columns, want 40", n)
	}
	// Start B is 11010010000: █ ▌ ▐ ... pairs 11 01 00 10 00 0_.
	if !strings.HasPrefix(lines[0], "█▐ ▌ ") {
		t.Errorf("row starts %q, want the start B pattern", lines[0])
	}
}

func TestRenderPropagatesEncodeErrors(t *testing.T) {
	if _, err := code128.Render("é", 1); err == nil {
		t.Error("Render accepted a character outside printable ASCII")
	}
}
