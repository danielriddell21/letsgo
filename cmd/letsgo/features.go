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
	disabled, err := configuredDisabled()
	if err != nil {
		return err
	}
	set := feature.Resolve(disabled)

	for _, f := range feature.All {
		on := set.On(f.Name)
		from := "default"
		if on != f.Default {
			from = plan.ConfigFile
		}
		fmt.Fprintf(w, "%-14s %-9s %-3s %-11s %s\n", f.Name, f.Kind, onOff(on), from, changeHint(f))
	}
	return nil
}

// configuredDisabled reads the repository's own disable directive, or none
// when there is no config file: zero-config is the primary path, not a
// problem.
func configuredDisabled() ([]string, error) {
	data, err := os.ReadFile(plan.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("letsgo: reading %s: %w", plan.ConfigFile, err)
	}

	file, err := config.Parse(filepath.Base(plan.ConfigFile), data)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Decode(file)
	if err != nil {
		return nil, err
	}
	return cfg.Disabled, nil
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
