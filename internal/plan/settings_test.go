package plan

import (
	"slices"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
)

func TestPlan_Choices(t *testing.T) {
	tests := []struct {
		value string
		want  Choice
	}{
		{"auto", Auto},
		{"", Auto},
		{"true", Yes},
		{"false", No},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			p := &Plan{Config: &config.Config{Prerelease: tt.value, Latest: tt.value}}
			if got := p.Prerelease(); got != tt.want {
				t.Errorf("Prerelease() = %v, want %v", got, tt.want)
			}
			if got := p.Latest(); got != tt.want {
				t.Errorf("Latest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlan_DraftAndStable(t *testing.T) {
	p := &Plan{}
	if p.Draft() {
		t.Fatal("a hand-built plan is not a draft")
	}
	p.MarkDraft()
	if !p.Draft() {
		t.Fatal("MarkDraft did not make the plan a draft")
	}
	p.Config.Prerelease = "true"
	p.MarkStable()
	if p.Draft() || p.Prerelease() != No {
		t.Errorf("MarkStable left Draft=%v Prerelease=%v", p.Draft(), p.Prerelease())
	}
}

func TestPlan_MarkStableDoesNotMutateTheSharedConfig(t *testing.T) {
	cfg := &config.Config{Draft: true}
	p := &Plan{Config: cfg}
	p.MarkStable()
	if !cfg.Draft {
		t.Error("MarkStable changed the config the plan was resolved from")
	}
}

func TestPlan_ConfigReaders(t *testing.T) {
	p := &Plan{Config: &config.Config{
		ModuleDir:   "sub",
		BrewCaveats: "needs a display",
		Variants:    []config.Variant{{Name: "a"}, {Name: "b"}},
	}}
	if p.ModuleDir() != "sub" || p.BrewCaveats() != "needs a display" {
		t.Errorf("ModuleDir=%q BrewCaveats=%q", p.ModuleDir(), p.BrewCaveats())
	}
	if got := p.VariantNames(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("VariantNames() = %v", got)
	}
	if got := (&Plan{}).VariantNames(); len(got) != 0 {
		t.Errorf("a plan with no config has variants %v", got)
	}
}
