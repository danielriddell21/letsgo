package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// VulncheckInstall is how to obtain the tool.
const VulncheckInstall = "go install golang.org/x/vuln/cmd/govulncheck@latest"

// Vulnerability is a finding in code the program can actually reach.
type Vulnerability struct {
	ID      string
	Package string
	Symbol  string
	FixedIn string
}

func (v Vulnerability) String() string {
	s := v.ID
	if v.Symbol != "" {
		s += " via " + v.Symbol
	}
	if v.FixedIn != "" {
		s += ", fixed in " + v.FixedIn
	}
	return s
}

// Vulncheck reports known vulnerabilities in code the module reaches.
//
// Reachability is what makes this worth gating on. A dependency-level scanner
// reports every advisory affecting anything in the module graph, most of which
// concern code the program never calls; gating on that produces a queue of
// findings nobody can action and a habit of overriding the gate. govulncheck
// traces from the program's own entry points, so a finding means this binary
// can execute the affected code.
func Vulncheck(ctx context.Context, dir string) ([]Vulnerability, error) {
	bin, err := find("govulncheck", VulncheckInstall)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, bin, "-format", "json", "./...")
	cmd.Dir = dir

	// govulncheck exits non-zero when it finds something, which is a result
	// rather than a failure.
	out, err := output(cmd, true)
	if err != nil {
		return nil, err
	}
	return parseVulncheck(strings.NewReader(string(out)))
}

// finding is the subset of govulncheck's output that matters here.
type finding struct {
	OSV          string `json:"osv"`
	FixedVersion string `json:"fixed_version"`
	Trace        []struct {
		Module   string `json:"module"`
		Package  string `json:"package"`
		Function string `json:"function"`
		Receiver string `json:"receiver"`
	} `json:"trace"`
}

// parseVulncheck reads govulncheck's JSON stream.
//
// The output is a sequence of objects, each carrying one of several keys. Only
// findings matter, and only those whose trace reaches a function: a finding
// without one means the vulnerable module is in the graph but the vulnerable
// code is not called.
func parseVulncheck(r io.Reader) ([]Vulnerability, error) {
	dec := json.NewDecoder(r)
	found := map[string]Vulnerability{}

	for {
		var message struct {
			Finding *finding `json:"finding"`
		}
		if err := dec.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("gate: reading govulncheck output: %w", err)
		}
		if message.Finding == nil || len(message.Finding.Trace) == 0 {
			continue
		}

		frame := message.Finding.Trace[0]
		if frame.Function == "" {
			continue
		}

		symbol := frame.Function
		if frame.Receiver != "" {
			symbol = frame.Receiver + "." + symbol
		}
		if frame.Package != "" {
			symbol = frame.Package + "." + symbol
		}

		// The same advisory can be reached by several paths; it is one
		// problem to fix.
		found[message.Finding.OSV] = Vulnerability{
			ID:      message.Finding.OSV,
			Package: frame.Package,
			Symbol:  symbol,
			FixedIn: message.Finding.FixedVersion,
		}
	}

	out := make([]Vulnerability, 0, len(found))
	for _, v := range found {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
