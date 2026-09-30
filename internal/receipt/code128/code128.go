// Package code128 encodes text as a Code 128 barcode in character set B.
//
// It exists to draw a decorative barcode on a terminal, so it stops at the
// encoding: the symbols, their check character, and a drawing of the bars in
// half-blocks. The encoding is the real one, but nothing promises a
// terminal's bars will scan.
package code128

import (
	"fmt"
	"strings"
)

const (
	startB = 104
	stop   = 106
)

// patterns holds each symbol's bar and space widths in modules, starting with
// a bar, indexed by symbol value. The stop symbol is the one thirteen-module
// pattern, which ends in a two-module bar that patterns omits.
var patterns = [...]string{
	"212222", "222122", "222221", "121223", "121322", "131222", "122213", "122312",
	"132212", "221213", "221312", "231212", "112232", "122132", "122231", "113222",
	"123122", "123221", "223211", "221132", "221231", "213212", "223112", "312131",
	"311222", "321122", "321221", "312212", "322112", "322211", "212123", "212321",
	"232121", "111323", "131123", "131321", "112313", "132113", "132311", "211313",
	"231113", "231311", "112133", "112331", "132131", "113123", "113321", "133121",
	"313121", "211331", "231131", "213113", "213311", "213131", "311123", "311321",
	"331121", "312113", "312311", "332111", "314111", "221411", "431111", "111224",
	"111422", "121124", "121421", "141122", "141221", "112214", "112412", "122114",
	"122411", "142112", "142211", "241211", "221114", "413111", "241112", "134111",
	"111242", "121142", "121241", "114212", "124112", "124211", "411212", "421112",
	"421211", "212141", "214121", "412121", "111143", "111341", "131141", "114113",
	"114311", "411113", "411311", "113141", "114131", "311141", "411131", "211412",
	"211214", "211232",
	"233111",
}

// Encode returns the symbols for text: the start character, one symbol per
// byte of text, the check character, and the stop character.
//
// Character set B covers printable ASCII, so anything else is an error.
func Encode(text string) ([]int, error) {
	symbols := []int{startB}
	sum := startB

	for i := 0; i < len(text); i++ {
		c := text[i]
		if c < ' ' || c > '~' {
			return nil, fmt.Errorf("code128: %q is not printable ASCII", c)
		}
		value := int(c - ' ')
		symbols = append(symbols, value)
		sum += value * (i + 1)
	}

	return append(symbols, sum%103, stop), nil
}

// Modules lays symbols out as bars (true) and spaces (false), one entry per
// module.
func Modules(symbols []int) []bool {
	var modules []bool
	for _, s := range symbols {
		bar := true
		for _, w := range patterns[s] {
			for range int(w - '0') {
				modules = append(modules, bar)
			}
			bar = !bar
		}
	}

	// The stop pattern's final bar is part of the symbol.
	return append(modules, true, true)
}

// Render draws text as a barcode of the given height in rows, two modules to
// a column using half-blocks. There is no quiet zone: what surrounds it is
// the terminal's own background.
func Render(text string, height int) (string, error) {
	symbols, err := Encode(text)
	if err != nil {
		return "", err
	}
	modules := Modules(symbols)

	var row strings.Builder
	for i := 0; i < len(modules); i += 2 {
		left := modules[i]
		right := i+1 < len(modules) && modules[i+1]
		row.WriteRune(cell(left, right))
	}

	lines := make([]string, max(height, 1))
	for i := range lines {
		lines[i] = row.String()
	}
	return strings.Join(lines, "\n"), nil
}

func cell(left, right bool) rune {
	switch {
	case left && right:
		return '█'
	case left:
		return '▌'
	case right:
		return '▐'
	default:
		return ' '
	}
}
