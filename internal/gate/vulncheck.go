package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// VulncheckInstall is how to obtain the tool.
const VulncheckInstall = "go install golang.org/x/vuln/cmd/govulncheck@latest"

// Vulnerability is a finding in code the program can actually reach.
type Vulnerability struct {
	ID      string
	Module  string
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
	report, err := VulncheckReport(ctx, dir)
	if err != nil {
		return nil, err
	}
	return report.Vulnerabilities, nil
}

// Report is a full govulncheck run: what it found, and the tool and database
// versions it ran with. audit records the versions; the release-time gate
// only needs the findings, which is what Vulncheck returns.
type Report struct {
	Vulnerabilities []Vulnerability

	// GovulncheckVersion and VulndbDate are empty when govulncheck's own
	// output did not report them.
	GovulncheckVersion string
	VulndbDate         string // YYYY-MM-DD
}

// VulncheckReport is Vulncheck, keeping the tool and database versions the
// run reported.
func VulncheckReport(ctx context.Context, dir string) (*Report, error) {
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
	return parseVulncheckReport(strings.NewReader(string(out)))
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

// vulncheckConfig is the subset of govulncheck's leading "config" message
// that names what it ran with.
type vulncheckConfig struct {
	ScannerVersion string     `json:"scanner_version"`
	DBLastModified *time.Time `json:"db_last_modified"`
}

// parseVulncheck reads govulncheck's JSON stream for its findings alone.
func parseVulncheck(r io.Reader) ([]Vulnerability, error) {
	report, err := parseVulncheckReport(r)
	if err != nil {
		return nil, err
	}
	return report.Vulnerabilities, nil
}

// parseVulncheckReport reads govulncheck's JSON stream.
//
// The output is a sequence of objects, each carrying one of several keys.
// Only two are read: the leading config message, for the tool and database
// versions, and findings whose trace reaches a function — a finding without
// one means the vulnerable module is in the graph but the vulnerable code is
// not called.
func parseVulncheckReport(r io.Reader) (*Report, error) {
	dec := json.NewDecoder(r)
	found := map[string]Vulnerability{}
	report := &Report{}

	for {
		var message struct {
			Config  *vulncheckConfig `json:"config"`
			Finding *finding         `json:"finding"`
		}
		if err := dec.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("gate: reading govulncheck output: %w", err)
		}

		if message.Config != nil {
			report.GovulncheckVersion = message.Config.ScannerVersion
			if message.Config.DBLastModified != nil {
				report.VulndbDate = message.Config.DBLastModified.Format("2006-01-02")
			}
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
			Module:  frame.Module,
			Package: frame.Package,
			Symbol:  symbol,
			FixedIn: message.Finding.FixedVersion,
		}
	}

	report.Vulnerabilities = make([]Vulnerability, 0, len(found))
	for _, v := range found {
		report.Vulnerabilities = append(report.Vulnerabilities, v)
	}
	sort.Slice(report.Vulnerabilities, func(i, j int) bool {
		return report.Vulnerabilities[i].ID < report.Vulnerabilities[j].ID
	})
	return report, nil
}
