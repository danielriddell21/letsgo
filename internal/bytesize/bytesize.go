// Package bytesize parses and renders sizes in bytes.
//
// One interpretation is used throughout: KB, MB, GB and TB are powers of
// 1024. Two conventions for the same word is how a 15MB budget becomes a
// 14.3MB budget somewhere between the config file and the error message, so
// the IEC spellings (KiB, MiB) are accepted as synonyms rather than as a
// second, quieter meaning.
package bytesize

import (
	"fmt"
	"strconv"
	"strings"
)

// Size is a number of bytes.
type Size int64

// Common sizes.
const (
	B Size = 1 << (10 * iota)
	KB
	MB
	GB
	TB
)

// units maps a suffix to its multiplier. Written longest-first where it
// matters so that "kib" is not matched as "b" with a "ki" prefix.
var units = []struct {
	suffix string
	size   Size
}{
	{"kib", KB}, {"mib", MB}, {"gib", GB}, {"tib", TB},
	{"kb", KB}, {"mb", MB}, {"gb", GB}, {"tb", TB},
	{"k", KB}, {"m", MB}, {"g", GB}, {"t", TB},
	{"b", B},
}

// Parse reads a size such as "15MB", "1.5 GiB" or "900000".
//
// A bare number is bytes. Fractions are allowed because "1.5MB" is how people
// write it, and are rounded to the nearest byte.
func Parse(s string) (Size, error) {
	text := strings.TrimSpace(s)
	if text == "" {
		return 0, fmt.Errorf("bytesize: empty size")
	}

	lower := strings.ToLower(text)
	multiplier := B
	for _, u := range units {
		if rest, ok := strings.CutSuffix(lower, u.suffix); ok {
			lower, multiplier = rest, u.size
			break
		}
	}

	number, err := strconv.ParseFloat(strings.TrimSpace(lower), 64)
	if err != nil {
		return 0, fmt.Errorf("bytesize: %q is not a size (try 15MB, 1.5GiB or a plain byte count)", s)
	}
	if number < 0 {
		return 0, fmt.Errorf("bytesize: %q is negative", s)
	}

	return Size(number*float64(multiplier) + 0.5), nil
}

// String renders a size the way a person reads one.
func (s Size) String() string {
	n := int64(s)
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	if n < int64(KB) {
		return fmt.Sprintf("%s%d B", sign, n)
	}

	div, exp := int64(KB), 0
	for n/div >= int64(KB) && exp < 3 {
		div *= int64(KB)
		exp++
	}
	return fmt.Sprintf("%s%.1f %cB", sign, float64(n)/float64(div), "KMGT"[exp])
}
