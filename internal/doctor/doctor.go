// Package doctor diagnoses the tools and repository state a release needs,
// read-only and offline: see docs/hld/doctor.md.
//
// Setup problems otherwise surface mid-release, or as a plan Skip that is
// easy to miss — govulncheck isn't installed, the wrong go is on PATH, the
// clone is shallow. Each one costs a CI run to discover instead.
package doctor

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// Status is the outcome of one check.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

func (s Status) symbol() string {
	switch s {
	case OK:
		return "✓"
	case Fail:
		return "✗"
	case Warn:
		return "!"
	default:
		return "?"
	}
}

// Check is one line of doctor's report.
type Check struct {
	Group  string
	Name   string
	Status Status
	Detail string
	Hint   string
}

// Result is everything doctor found.
type Result struct {
	Checks []Check
}

// OK reports whether every check passed.
func (r *Result) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// add records one tools check. Every check is in the "tools" group so far;
// repository checks (Phase 2) will need a group parameter back.
func (r *Result) add(name string, status Status, hint, format string, args ...any) {
	r.Checks = append(r.Checks, Check{
		Group: "tools", Name: name, Status: status,
		Detail: fmt.Sprintf(format, args...), Hint: hint,
	})
}

// Run diagnoses the module at dir. It makes no network calls and writes
// nothing (DR-10); an error means the module itself could not be found, the
// same requirement every other command already has.
func Run(ctx context.Context, dir string) (*Result, error) {
	root, err := discover.FindModule(dir)
	if err != nil {
		return nil, err
	}

	r := &Result{}
	r.checkGo(ctx, root)
	r.checkGit(ctx)
	r.checkVulncheck(requiresVulncheck(root.Dir))
	r.checkApidiff()
	return r, nil
}

// requiresVulncheck reports whether letsgo.mod's `require` directive names
// vulncheck (DR-9) — read the same way plan.Resolve reads it
// (config.Parse/Decode), so doctor and plan never disagree. A missing or
// malformed letsgo.mod requires nothing here; letsgo.mod's own validity is a
// repository check, not a tools one.
func requiresVulncheck(moduleDir string) bool {
	data, err := os.ReadFile(filepath.Join(moduleDir, plan.ConfigFile))
	if err != nil {
		return false
	}
	file, err := config.Parse(plan.ConfigFile, data)
	if err != nil {
		return false
	}
	cfg, err := config.Decode(file)
	if err != nil {
		return false
	}
	for _, name := range cfg.Required {
		if name == "vulncheck" {
			return true
		}
	}
	return false
}

// checkGo reports the resolved go toolchain (DR-1, DR-2), and whether it
// matches what go.mod requires (DR-3).
func (r *Result) checkGo(ctx context.Context, root discover.Module) {
	path, _, err := gobuild.ToolchainSource()
	if err != nil {
		r.add("go", Fail, "", "%v", err)
		return
	}

	version, err := gobuild.Version(ctx, path)
	if err != nil {
		r.add("go", Fail, "", "%s: %v", path, err)
		return
	}
	r.add("go", OK, "", "%s (%s)", version, path)

	want, exact, err := discover.GoDirective(filepath.Join(root.Dir, "go.mod"))
	if err != nil || versionSatisfies(want, exact, version) {
		return
	}
	r.add("go.mod", Warn, "", "wants %s; GOTOOLCHAIN will switch", want)
}

// versionSatisfies reports whether got (what the local go reports) meets
// what want requires. A toolchain directive pins an exact version — GOTOOLCHAIN
// switches to it on anything else. A go directive is a minimum: any version
// at least that high needs no switch, matching what `go` itself decides.
func versionSatisfies(want string, exact bool, got string) bool {
	w, ok := parseGoVersion(want)
	if !ok {
		return true // can't parse what go.mod wants; nothing to warn about
	}
	g, ok := parseGoVersion(got)
	if !ok {
		return true
	}
	if exact {
		return semver.Compare(g, w) == 0
	}
	return semver.Compare(g, w) >= 0
}

// parseGoVersion parses a "goX.Y[.Z]" string, defaulting a missing patch to
// zero the way go.mod's own two-part go directive means "any patch".
func parseGoVersion(s string) (semver.Version, bool) {
	s = strings.TrimPrefix(s, "go")
	if strings.Count(s, ".") == 1 {
		s += ".0"
	}
	return semver.Parse(s)
}

// checkGit reports the resolved git binary (DR-1, DR-2).
func (r *Result) checkGit(ctx context.Context) {
	path, _, err := discover.GitSource()
	if err != nil {
		r.add("git", Fail, "", "%v", err)
		return
	}

	version := "unknown version"
	if out, err := exec.CommandContext(ctx, path, "--version").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			version = strings.TrimPrefix(v, "git version ")
		}
	}
	r.add("git", OK, "", "%s (%s)", version, path)
}

// checkVulncheck reports whether govulncheck is installed (DR-1, DR-2),
// escalated to Fail under `require vulncheck` (DR-9).
func (r *Result) checkVulncheck(required bool) {
	r.checkGateTool("govulncheck", gate.VulncheckInstall, required)
}

// checkApidiff reports whether apidiff is installed (DR-1, DR-2). Nothing in
// the HLD asks a missing apidiff to escalate under any `require` directive.
func (r *Result) checkApidiff() {
	r.checkGateTool("apidiff", gate.ApidiffInstall, false)
}

// checkGateTool resolves a gate tool through gate.Find — the same search the
// gate that runs it uses (DR-11) — and reports its version, read from the
// binary's own embedded build info rather than by running it, so a version
// check can never itself make a network call (DR-10).
func (r *Result) checkGateTool(name, install string, required bool) {
	path, err := gate.Find(name, install)
	if err != nil {
		status := Warn
		if required {
			status = Fail
		}
		r.add(name, status, install, "not installed")
		return
	}

	detail := path
	if version := binaryVersion(path); version != "" {
		detail = fmt.Sprintf("%s (%s)", version, path)
	}
	r.add(name, OK, "", "%s", detail)
}

// binaryVersion reads a Go binary's embedded module version, if any, without
// running it.
func binaryVersion(path string) string {
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return ""
	}
	return info.Main.Version
}
