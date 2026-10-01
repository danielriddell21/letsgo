package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/config"
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
func Vulncheck(ctx context.Context, global *config.Global, dir string) ([]Vulnerability, error) {
	report, err := VulncheckReport(ctx, global, dir)
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

// Scan is one build configuration to check a module under. It selects which
// files compile, and so which code is reachable: a finding behind a build tag
// or in another platform's files is only real for the configuration that
// compiles it.
type Scan struct {
	// Tags are build tags, passed to govulncheck as -tags.
	Tags []string

	// Env is extra environment for the run, as KEY=VALUE pairs: GOOS, GOARCH
	// and CGO_ENABLED, as the release was built with.
	Env []string
}

// VulncheckReport is Vulncheck, keeping the tool and database versions the
// run reported.
//
// With no scans the module is checked as the host would build it. With
// several, each is run and the findings merged, so a release shipped for many
// targets is checked for all of them.
func VulncheckReport(ctx context.Context, global *config.Global, dir string, scans ...Scan) (*Report, error) {
	bin, err := Find(global, "govulncheck", VulncheckInstall)
	if err != nil {
		return nil, err
	}
	if len(scans) == 0 {
		scans = []Scan{{}}
	}

	reports := make([]*Report, 0, len(scans))
	for _, scan := range scans {
		args := []string{"-format", "json"}
		if len(scan.Tags) > 0 {
			args = append(args, "-tags", strings.Join(scan.Tags, ","))
		}
		cmd := exec.CommandContext(ctx, bin, append(args, "./...")...)
		cmd.Dir = dir
		if len(scan.Env) > 0 {
			cmd.Env = append(os.Environ(), scan.Env...)
		}

		// govulncheck exits non-zero when it finds something, which is a
		// result rather than a failure.
		out, err := output(cmd, true)
		if err != nil {
			return nil, err
		}
		report, err := parseVulncheckReport(strings.NewReader(string(out)))
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return mergeReports(reports), nil
}

// mergeReports combines per-scan reports into one. The tool and database
// versions come from the first report that names them; an advisory reached
// under several scans is one finding.
func mergeReports(reports []*Report) *Report {
	if len(reports) == 1 {
		return reports[0]
	}
	merged := &Report{}
	seen := map[string]bool{}
	for _, r := range reports {
		if merged.GovulncheckVersion == "" {
			merged.GovulncheckVersion = r.GovulncheckVersion
		}
		if merged.VulndbDate == "" {
			merged.VulndbDate = r.VulndbDate
		}
		for _, v := range r.Vulnerabilities {
			if !seen[v.ID] {
				seen[v.ID] = true
				merged.Vulnerabilities = append(merged.Vulnerabilities, v)
			}
		}
	}
	if merged.Vulnerabilities == nil {
		merged.Vulnerabilities = []Vulnerability{}
	}
	sort.Slice(merged.Vulnerabilities, func(i, j int) bool {
		return merged.Vulnerabilities[i].ID < merged.Vulnerabilities[j].ID
	})
	return merged
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
