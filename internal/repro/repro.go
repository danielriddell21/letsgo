// Package repro drives the release pipeline for reproducibility testing.
//
// It deliberately contains no build logic of its own. Everything it exercises
// lives in internal/build, so that a green cross-machine CI run is evidence
// about the code letsgo actually ships rather than about a parallel copy
// maintained alongside it.
package repro

import (
	"context"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// Options and Artifact are the pipeline's own types, re-exported so that test
// code reads as one thing rather than two.
type (
	Options  = build.Options
	Artifact = build.Artifact
)

// DefaultTargets covers both archive formats and both executable conventions,
// which is the minimum that meaningfully exercises the pipeline.
var DefaultTargets = []gobuild.Target{
	{OS: "linux", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

// Build compiles and packages every target.
func Build(ctx context.Context, o Options) ([]Artifact, error) {
	if len(o.Targets) == 0 {
		o.Targets = DefaultTargets
	}
	return build.Run(ctx, o)
}
