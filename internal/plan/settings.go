package plan

import (
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/github"
)

// Choice is a setting that is either decided or left to letsgo: the typed
// reading of a config value that is "auto", "true" or "false" on disk, so a
// consumer never compares raw strings.
type Choice int

const (
	// Auto leaves the decision to letsgo.
	Auto Choice = iota
	// Yes is an explicit "true".
	Yes
	// No is an explicit "false".
	No
)

func choiceOf(value string) Choice {
	switch value {
	case "true":
		return Yes
	case "false":
		return No
	}
	return Auto
}

// settings is the config the plan resolved from, or an empty one for a plan
// that was built by hand rather than resolved.
func (p *Plan) settings() *config.Config {
	if p.Config == nil {
		return &config.Config{}
	}
	return p.Config
}

// Draft reports whether the release is published as a draft, whether the
// config or `release --draft` asked for it.
func (p *Plan) Draft() bool { return p.settings().Draft }

// HasTap reports whether the release publishes to a Homebrew tap.
func (p *Plan) HasTap() bool { return p.Tap != (github.Repo{}) }

// MarkDraft makes the release a draft, as `release --draft` does.
func (p *Plan) MarkDraft() { p.ensureConfig().Draft = true }

// MarkStable makes the release a public, stable one whatever the config says
// about drafts and prereleases, which is what promoting a candidate means.
func (p *Plan) MarkStable() {
	cfg := *p.settings()
	cfg.Draft, cfg.Prerelease = false, "false"
	p.Config = &cfg
}

func (p *Plan) ensureConfig() *config.Config {
	if p.Config == nil {
		p.Config = &config.Config{}
	}
	return p.Config
}

// Prerelease is the config's `prerelease` setting: Auto defers to the version.
func (p *Plan) Prerelease() Choice { return choiceOf(p.settings().Prerelease) }

// Latest is the config's `latest` setting: Auto defers to the module's scope.
func (p *Plan) Latest() Choice { return choiceOf(p.settings().Latest) }

// ModuleDir is the module's directory relative to the repository's letsgo.mod
// when `module` points at another one, and empty for the module beside it.
func (p *Plan) ModuleDir() string { return p.settings().ModuleDir }

// BrewCaveats is the formula's caveats block.
func (p *Plan) BrewCaveats() string { return p.settings().BrewCaveats }

// VariantNames are the names of the variants this release builds, in order.
func (p *Plan) VariantNames() []string {
	variants := p.settings().Variants
	names := make([]string, 0, len(variants))
	for _, v := range variants {
		names = append(names, v.Name)
	}
	return names
}
