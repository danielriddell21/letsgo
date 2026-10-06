package plan

import (
	"errors"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/feature"
)

func (p *Plan) loadConfig(moduleDir string) {
	cfg, path, err := config.Load(moduleDir)
	switch {
	case errors.Is(err, config.ErrNotFound):
		// Absence is the primary path, not a problem.
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.note("config", "none", "zero-config defaults")
		return
	case err != nil:
		p.Config = &config.Config{Budgets: map[string]string{}}
		p.add("config", Fail, "%v", err)
		return
	}

	p.Config, p.ConfigPath = cfg, path
	p.note("config", config.FileName, "repository root")
}

// resolveFeatures folds letsgo.mod's `disable` directive together with any
// one-run flag that means the same thing, so the rest of the plan has one
// place to ask whether a feature is on.
func (p *Plan) resolveFeatures(opts Options) {
	disabled := append([]string(nil), p.Config.Disabled...)

	from := config.FileName
	if opts.DisableProxyWarm {
		if len(disabled) == 0 {
			from = "--no-proxy-warm"
		} else {
			from += ", --no-proxy-warm"
		}
		disabled = append(disabled, "proxy-warm")
	}

	p.Features = feature.Resolve(disabled)
	p.Required = append([]string(nil), p.Config.Required...)
	sort.Strings(p.Required)

	if len(p.Features) == 0 && len(p.Required) == 0 {
		return
	}

	var parts []string
	if len(p.Features) > 0 {
		parts = append(parts, "disabled: "+strings.Join(p.Features.Disabled(), ", "))
	}
	if len(p.Required) > 0 {
		parts = append(parts, "required: "+strings.Join(p.Required, ", "))
	}
	detail := strings.Join(parts, "; ")

	p.note("features", detail, from)
	p.add("features", Pass, "%s", detail)
}

// required reports whether a feature's Skip must be a Fail instead.
func (p *Plan) required(name feature.Name) bool {
	for _, n := range p.Required {
		if n == string(name) {
			return true
		}
	}
	return false
}

// skip records a check as Skip, unless the feature behind it is required —
// in which case a Skip is exactly the outcome require promised would not
// happen.
func (p *Plan) skip(check string, feat feature.Name, format string, args ...any) {
	if p.required(feat) {
		p.addAt(p.posOf("require "+string(feat)), check, Fail, format, args...)
		return
	}
	p.add(check, Skip, format, args...)
}
