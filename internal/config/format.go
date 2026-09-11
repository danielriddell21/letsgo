package config

import (
	"strconv"
	"strings"
)

// Format renders a file in canonical form.
//
// letsgo fmt exists for the same reason go fmt does: configuration that is
// formatted by a tool cannot drift stylistically between repositories, and
// reviewing a config change should never mean reading a whitespace diff.
func (f *File) Format() []byte {
	var b strings.Builder

	previousBlank := true // suppresses leading blank lines

	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *Comment:
			if s.Blank {
				// Runs of blank lines carry no more meaning than one does.
				if previousBlank {
					continue
				}
				b.WriteString("\n")
				previousBlank = true
				continue
			}
			b.WriteString("// " + s.Text + "\n")
			previousBlank = false

		case *Line:
			b.WriteString(s.Keyword)
			for _, arg := range s.Args {
				b.WriteString(" " + quoteIfNeeded(arg))
			}
			writeComment(&b, s.Comment)
			b.WriteString("\n")
			previousBlank = false

		case *Block:
			b.WriteString(s.Keyword + " (")
			writeComment(&b, s.Comment)
			b.WriteString("\n")
			for _, line := range s.Lines {
				b.WriteString("\t")
				for i, arg := range line.Args {
					if i > 0 {
						b.WriteString(" ")
					}
					b.WriteString(quoteIfNeeded(arg))
				}
				writeComment(&b, line.Comment)
				b.WriteString("\n")
			}
			b.WriteString(")\n")
			previousBlank = false
		}
	}

	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return nil
	}
	return []byte(out + "\n")
}

func writeComment(b *strings.Builder, comment string) {
	if comment != "" {
		b.WriteString(" // " + comment)
	}
}

// quoteIfNeeded quotes an argument that would not survive a round trip
// unquoted. Anything else is left alone: a format that gratuitously quotes is
// a format people fight with.
func quoteIfNeeded(arg string) string {
	if arg == "" {
		return `""`
	}
	if strings.ContainsAny(arg, " \t\"\\") || strings.Contains(arg, "//") ||
		arg == "(" || arg == ")" {
		return strconv.Quote(arg)
	}
	return arg
}
