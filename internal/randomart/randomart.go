// Package randomart draws a digest as a drunken-bishop picture, the same
// fingerprint art OpenSSH prints for `ssh-keygen -lv`. Two digests that differ
// anywhere draw pictures that differ at a glance, so a person can compare a
// release page against their own verify run without reading hex.
package randomart

import "strings"

const (
	width  = 17
	height = 9

	// glyphs is indexed by visit count, with the start and end squares last.
	glyphs = " .o+=*BOX@%&#/^SE"
)

// Render draws digest in a 17x9 field under a header carrying title, with the
// walk OpenSSH's fingerprint_randomart takes: two bits per move, least
// significant pair first, sliding along the walls. The footer names the hash.
// The picture is the nine field rows between the frame lines, so it matches
// ssh-keygen byte for byte for the same digest; only the titles differ.
func Render(digest []byte, title string) string {
	field := walk(digest)

	var out strings.Builder
	out.WriteString(frame(title))
	out.WriteByte('\n')
	for row := range height {
		out.WriteByte('|')
		for col := range width {
			out.WriteByte(glyphs[field[col][row]])
		}
		out.WriteString("|\n")
	}
	out.WriteString(frame("SHA256"))
	return out.String()
}

// walk moves the bishop across the digest and returns each square's visit
// count, with the start and end squares set to their own glyphs.
func walk(digest []byte) (field [width][height]int) {
	x, y := width/2, height/2
	for _, b := range digest {
		for range 4 {
			x = min(max(x+step(b&1), 0), width-1)
			y = min(max(y+step(b&2), 0), height-1)
			field[x][y] = min(field[x][y]+1, len(glyphs)-3)
			b >>= 2
		}
	}
	field[width/2][height/2] = len(glyphs) - 2
	field[x][y] = len(glyphs) - 1
	return field
}

// step is +1 when the move's bit is set and -1 when it is clear.
func step(bit byte) int {
	if bit != 0 {
		return 1
	}
	return -1
}

// frame is a border line with text in square brackets, truncated to fit.
func frame(text string) string {
	label := "[" + text + "]"
	if len(label) > width {
		label = label[:width]
	}
	pad := width - len(label)
	left := (width - len(label)) / 2
	return "+" + strings.Repeat("-", left) + label + strings.Repeat("-", pad-left) + "+"
}

// Title is the header text for a release: "letsgo <tag>" when that fits the
// frame, else the tag alone, which the frame truncates if it is longer still.
func Title(tag string) string {
	if title := "letsgo " + tag; len(title)+2 <= width {
		return title
	}
	return tag
}
