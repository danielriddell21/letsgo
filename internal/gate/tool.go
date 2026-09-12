// Package gate runs the correctness checks that decide whether a release is
// fit to publish.
//
// These are the checks only a Go-only tool can afford to make: the language
// has a first-party vulnerability database keyed to its modules, and an
// exported API that can be compared between two versions. Both are read by
// running the tools the Go project already ships for the purpose.
package gate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrToolMissing reports that a check's program is not installed.
//
// Distinguished from a failing check because the two mean opposite things: an
// absent tool is a gate that did not run, and treating it as a pass would
// claim a guarantee nothing established.
var ErrToolMissing = errors.New("gate: tool not installed")

// MissingToolError names what to install.
type MissingToolError struct {
	Tool    string
	Install string
}

func (e *MissingToolError) Error() string {
	return fmt.Sprintf("%s is not installed\n  install it with: %s", e.Tool, e.Install)
}

func (e *MissingToolError) Unwrap() error { return ErrToolMissing }

// find locates a Go tool.
//
// Unlike git, which letsgo resolves only within system directories, these live
// wherever the user installed them — GOBIN, or GOPATH/bin, both of which are
// user-writable by design. There is no stricter search to perform: a tool the
// user chose to install is a tool they chose to trust, and refusing to look
// where Go puts things would mean never finding them.
func find(tool, install string) (string, error) {
	var candidates []string

	if gobin := os.Getenv("GOBIN"); gobin != "" {
		candidates = append(candidates, filepath.Join(gobin, tool))
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		for _, p := range filepath.SplitList(gopath) {
			candidates = append(candidates, filepath.Join(p, "bin", tool))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "go", "bin", tool))
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}

	if path, err := exec.LookPath(tool); err == nil {
		return path, nil
	}
	return "", &MissingToolError{Tool: tool, Install: install}
}

// output runs a tool and returns its standard output, treating a non-zero
// exit as success when the tool uses it to report findings.
func output(cmd *exec.Cmd, exitMeansFindings bool) ([]byte, error) {
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}

	var exit *exec.ExitError
	if exitMeansFindings && errors.As(err, &exit) && len(out) > 0 {
		return out, nil
	}

	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		return nil, fmt.Errorf("gate: %s: %w", filepath.Base(cmd.Path), err)
	}
	return nil, fmt.Errorf("gate: %s: %w\n%s", filepath.Base(cmd.Path), err, detail)
}
