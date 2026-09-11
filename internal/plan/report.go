package plan

import (
	"fmt"
	"io"
	"strings"
)

func (s Status) symbol() string {
	switch s {
	case Pass:
		return "✓"
	case Fail:
		return "✗"
	case Warn:
		return "!"
	default:
		return "·"
	}
}

// Report writes a human-readable summary of the plan.
//
// With explain set it also lists where every resolved value came from. Zero
// config is only trustworthy if the inference behind it can be inspected;
// without that, the primary path is indistinguishable from magic.
func (p *Plan) Report(w io.Writer, explain bool) {
	version := p.Version
	if version == "" {
		version = "(unresolved)"
	}
	fmt.Fprintf(w, "%s %s · commit %s\n\n", p.Project, version, p.Git.ShortCommit)

	if explain {
		fmt.Fprintln(w, "  resolved")
		width := 0
		for _, s := range p.Sources {
			width = max(width, len(s.Field))
		}
		for _, s := range p.Sources {
			fmt.Fprintf(w, "    %-*s  %s\n", width, s.Field, s.Value)
			fmt.Fprintf(w, "    %-*s  └─ from %s\n", width, "", s.From)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "  gates")
	width := 0
	for _, c := range p.Checks {
		width = max(width, len(c.Name))
	}
	for _, c := range p.Checks {
		lines := strings.Split(c.Detail, "\n")
		fmt.Fprintf(w, "    %s %-*s  %s\n", c.Status.symbol(), width, c.Name, lines[0])
		for _, extra := range lines[1:] {
			fmt.Fprintf(w, "      %-*s  %s\n", width, "", extra)
		}
	}

	if len(p.Artifacts) > 0 {
		fmt.Fprintf(w, "\n  artifacts (%d)\n", len(p.Artifacts)+1)
		for _, a := range p.Artifacts {
			fmt.Fprintf(w, "    %s\n", a.Name)
		}
		fmt.Fprintln(w, "    SHA256SUMS")
	}
}
