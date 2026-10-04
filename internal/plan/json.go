package plan

import (
	"encoding/json"
	"fmt"
)

// jsonResult is Plan's wire form for `letsgo plan --json`: schema-versioned
// (ED-13), so a consumer can tell which shape it's reading before the fields
// under it ever change.
type jsonResult struct {
	Schema    int                   `json:"schema"`
	Project   string                `json:"project"`
	Version   string                `json:"version,omitempty"`
	Commit    string                `json:"commit"`
	Resolved  []Provenance          `json:"resolved,omitempty"`
	Checks    []Check               `json:"checks"`
	Artifacts []string              `json:"artifacts,omitempty"`
	Features  jsonFeatures          `json:"features"`
	Plugins   map[string]jsonPlugin `json:"plugins,omitempty"`
}

// jsonFeatures mirrors manifest.Features's own wire shape, so a reader
// already parsing a published manifest recognises this one too.
type jsonFeatures struct {
	Disabled []string `json:"disabled,omitempty"`
	Required []string `json:"required,omitempty"`
}

type jsonPlugin struct {
	Command string `json:"command"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// JSON renders the plan for machine consumers (`letsgo plan --json`): the
// resolved values, gates, artifacts, features and plugins an editor needs
// (ED-7), in one call rather than one per surface.
func (p *Plan) JSON() ([]byte, error) {
	artifacts := make([]string, len(p.Artifacts))
	for i, a := range p.Artifacts {
		artifacts[i] = a.Name
	}

	var plugins map[string]jsonPlugin
	if len(p.Plugins) > 0 {
		plugins = make(map[string]jsonPlugin, len(p.Plugins))
		for hook, pl := range p.Plugins {
			plugins[string(hook)] = jsonPlugin{Command: pl.Command, Version: pl.Version, Digest: pl.Digest}
		}
	}

	data, err := json.MarshalIndent(jsonResult{
		Schema:    1,
		Project:   p.Project,
		Version:   p.Version,
		Commit:    p.Git.ShortCommit,
		Resolved:  p.Sources,
		Checks:    p.Checks,
		Artifacts: artifacts,
		Features:  jsonFeatures{Disabled: p.Features.Disabled(), Required: p.Required},
		Plugins:   plugins,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return data, nil
}
