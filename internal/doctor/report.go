package doctor

import (
	"fmt"
	"io"
)

// Report writes a human-readable summary, grouped and ordered as the checks
// were added, matching the group order the CLI is documented with.
func (r *Result) Report(w io.Writer) {
	width := 0
	for _, c := range r.Checks {
		width = max(width, len(c.Name))
	}

	group := ""
	for _, c := range r.Checks {
		if c.Group != group {
			if group != "" {
				fmt.Fprintln(w)
			}
			group = c.Group
			fmt.Fprintf(w, "  %s\n", group)
		}

		detail := c.Detail
		if c.Hint != "" {
			detail = fmt.Sprintf("%s — %s", detail, c.Hint)
		}
		fmt.Fprintf(w, "  %s %-*s %s\n", c.Status.symbol(), width, c.Name, detail)
	}
}
