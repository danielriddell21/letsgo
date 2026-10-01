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
	"encoding/json"
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
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// Result is everything doctor found.
type Result struct {
	Checks []Check
}

// jsonResult is Result's wire form: schema-versioned, so a consumer can tell
// which shape it's reading before the fields under it ever change.
type jsonResult struct {
	Schema int     `json:"schema"`
	Checks []Check `json:"checks"`
}

// JSON renders the report for machine consumers (`letsgo doctor --json`).
func (r *Result) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(jsonResult{Schema: 1, Checks: r.Checks}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("doctor: %w", err)
	}
	return data, nil
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

// The two groups doctor reports in, in the order they are printed.
const (
	toolsGroup      = "tools"
	repositoryGroup = "repository"
)

// add records one check.
func (r *Result) add(group, name string, status Status, hint, format string, args ...any) {
	r.Checks = append(r.Checks, Check{
		Group: group, Name: name, Status: status,
		Detail: fmt.Sprintf(format, args...), Hint: hint,
	})
}

// Run diagnoses the module at dir. It makes no network calls and writes
// nothing (DR-10); an error means the module or its repository could not be
// found at all — the same requirement every other command already has —
// rather than a problem with what was found there, which becomes a check
// instead so one run reports every issue.
func Run(ctx context.Context, dir string, global *config.Global) (*Result, error) {
	root, err := discover.FindModule(dir)
	if err != nil {
		return nil, err
	}
	git, err := discover.FindGit(ctx, root.Dir)
	if err != nil {
		return nil, err
	}
	scope, err := discover.NewScope(git.TopLevel, root.Dir)
	if err != nil {
		return nil, err
	}

	cfg, cfgErr := loadConfig(root.Dir)

	r := &Result{}
	r.checkGo(ctx, root, global)
	r.checkGit(ctx)
	r.checkVulncheck(cfgErr == nil && requiresVulncheck(cfg))
	r.checkApidiff()

	r.checkConfig(cfgErr)
	if cfgErr == nil {
		r.checkPlugins(cfg, root.Dir, global.PluginsDir)
	}
	r.checkHistory(ctx, root.Dir, git, scope)
	r.checkRemote(ctx, root.Dir)
	r.checkWorktree(git)
	return r, nil
}

// loadConfig reads letsgo.mod the same way plan.Resolve does
// (config.Parse/Decode): a missing file is the primary path, not an error;
// a parse or decode failure is returned for checkConfig to report (DR-4).
func loadConfig(moduleDir string) (*config.Config, error) {
	data, err := os.ReadFile(filepath.Join(moduleDir, plan.ConfigFile))
	if err != nil {
		return &config.Config{}, nil //nolint:nilerr // absence is not a failure
	}
	file, err := config.Parse(plan.ConfigFile, data)
	if err != nil {
		return nil, err
	}
	return config.Decode(file)
}

// requiresVulncheck reports whether letsgo.mod's `require` directive names
// vulncheck (DR-9), so doctor and plan never disagree about it.
func requiresVulncheck(cfg *config.Config) bool {
	for _, name := range cfg.Required {
		if name == "vulncheck" {
			return true
		}
	}
	return false
}

// checkGo reports the resolved go toolchain (DR-1, DR-2), and whether it
// matches what go.mod requires (DR-3).
func (r *Result) checkGo(ctx context.Context, root discover.Module, global *config.Global) {
	path, _, err := gobuild.Toolchain(global)
	if err != nil {
		r.add(toolsGroup, "go", Fail, "", "%v", err)
		return
	}

	version, err := gobuild.Version(ctx, path)
	if err != nil {
		r.add(toolsGroup, "go", Fail, "", "%s: %v", path, err)
		return
	}
	r.add(toolsGroup, "go", OK, "", "%s (%s)", version, path)

	want, exact, err := discover.GoDirective(filepath.Join(root.Dir, "go.mod"))
	if err != nil || versionSatisfies(want, exact, version) {
		return
	}
	r.add(toolsGroup, "go.mod", Warn, "", "wants %s; GOTOOLCHAIN will switch", want)
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
		r.add(toolsGroup, "git", Fail, "", "%v", err)
		return
	}

	version := "unknown version"
	if out, err := exec.CommandContext(ctx, path, "--version").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			version = strings.TrimPrefix(v, "git version ")
		}
	}
	r.add(toolsGroup, "git", OK, "", "%s (%s)", version, path)
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
		r.add(toolsGroup, name, status, install, "not installed")
		return
	}

	detail := path
	if version := binaryVersion(path); version != "" {
		detail = fmt.Sprintf("%s (%s)", version, path)
	}
	r.add(toolsGroup, name, OK, "", "%s", detail)
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
