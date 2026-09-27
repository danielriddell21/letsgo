package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/plan"
)

func runFeatures(args []string) error {
	fs := flag.NewFlagSet("features", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return listFeatures(os.Stdout)
}

// listFeatures prints the catalogue: what each feature is called, its state
// in this repository, where that state came from, and how to change it.
func listFeatures(w io.Writer) error {
	cfg, err := loadFeaturesConfig()
	if err != nil {
		return err
	}
	set := feature.Resolve(cfg.Disabled)
	required := make(map[string]bool, len(cfg.Required))
	for _, name := range cfg.Required {
		required[name] = true
	}

	for _, f := range feature.All {
		// Set.On only means "not disabled", which describes a feature that is
		// on unless told otherwise. One that is off unless told otherwise is
		// on only when its own directive actually appears.
		on := set.On(f.Name)
		if !f.Default {
			on = enabledByDirective(cfg, f.Name)
		}
		state := onOff(on)
		if required[f.Name] {
			state += ", required"
		}

		from := "default"
		if on != f.Default || required[f.Name] {
			from = plan.ConfigFile
		}
		fmt.Fprintf(w, "%-14s %-9s %-14s %-11s %s\n", f.Name, f.Kind, state, from, changeHint(f))
	}
	return nil
}

// loadFeaturesConfig reads the repository's own disable/require directives,
// or a zero Config when there is none: zero-config is the primary path, not
// a problem.
func loadFeaturesConfig() (*config.Config, error) {
	data, err := os.ReadFile(plan.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &config.Config{}, nil
		}
		return nil, fmt.Errorf("letsgo: reading %s: %w", plan.ConfigFile, err)
	}

	file, err := config.Parse(filepath.Base(plan.ConfigFile), data)
	if err != nil {
		return nil, err
	}
	return config.Decode(file)
}

// enabledByDirective reports whether a feature that is off by default has
// been turned on the only way it can be: by writing the directive that is
// its own switch.
func enabledByDirective(cfg *config.Config, name string) bool {
	switch name {
	case "budget":
		return len(cfg.Budgets) > 0
	case "brew":
		return cfg.BrewTap != ""
	case "image":
		return cfg.Image != nil
	default:
		return false
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// changeHint says what a repository would write to move a feature off its
// default, so the catalogue is also the answer to "how do I change this".
func changeHint(f feature.Feature) string {
	if f.Enable != "" {
		return fmt.Sprintf("enable with `%s`", f.Enable)
	}

	var how []string
	if f.Disable {
		how = append(how, "disable")
	}
	if f.Require {
		how = append(how, "require")
	}
	if len(how) == 0 {
		return "cannot be changed"
	}
	return strings.Join(how, ", ")
}
