package plan

import (
	"fmt"
	"strings"
)

// Markdown renders a plan for a job summary.
//
// The heading and the footer sit outside a diff block. Inside it every line
// starts in column 0 with its marker, because that is where a diff
// highlighter looks: + is coloured as an addition and - as a removal. A
// change is written ! rather than ~, which highlighters leave plain.
//
// Actions that keep their target are left out of the block and counted in
// the footer, so a summary shows what would happen and not everything that
// would not.
func Markdown(title string, actions []Action) string {
	var b strings.Builder
	b.WriteString("## " + title + "\n\n")

	kept := 0
	var lines []string
	kindWidth, targetWidth := 0, 0
	for _, a := range actions {
		if a.Op == Keep {
			kept++
			continue
		}
		kindWidth = max(kindWidth, len(a.Kind))
		targetWidth = max(targetWidth, len(a.Target))
	}
	for _, a := range actions {
		if a.Op == Keep {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %-*s  %-*s  %s",
			marker(a.Op), kindWidth, a.Kind, targetWidth, a.Target, detail(a)))
	}

	if len(lines) > 0 {
		b.WriteString("```diff\n")
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
		b.WriteString("```\n\n")
	}

	b.WriteString(Summary(actions))
	if kept > 0 {
		fmt.Fprintf(&b, " %d unchanged.", kept)
	}
	b.WriteString("\n")
	return b.String()
}

// marker is the character a diff highlighter colours an action by.
func marker(op Op) string {
	if op == Change {
		return "!"
	}
	return string(op)
}
