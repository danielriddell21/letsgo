// Package repro drives the release pipeline for reproducibility testing.
//
// It deliberately contains no build logic of its own. Everything it exercises
// lives in internal/build, so that a green cross-machine CI run is evidence
// about the code letsgo actually ships rather than about a parallel copy
// maintained alongside it.
package repro

import (
	"context"
	"fmt"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/oci"
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

// ImageDigest assembles a scratch image from the Linux artifacts and returns
// the index digest.
//
// Included in the reproducibility record because the image is published with
// the same promise as everything else: a digest that depends on the commit and
// nothing about the machine. Without this the claim would rest on the layer
// writer being shared, which is an argument rather than a check.
func ImageDigest(artifacts []Artifact, name string, created time.Time) (string, error) {
	var images []*oci.Image

	for _, a := range artifacts {
		if a.OS != "linux" {
			continue
		}
		image, err := oci.BuildImage(oci.ImageOptions{
			Binary:   a.BinaryPath,
			Name:     name,
			Platform: oci.Platform{OS: a.OS, Architecture: a.Arch},
			Created:  created,
		})
		if err != nil {
			return "", err
		}
		images = append(images, image)
	}
	if len(images) == 0 {
		return "", fmt.Errorf("repro: no linux artifact to build an image from")
	}

	index, err := oci.BuildIndex(images, nil)
	if err != nil {
		return "", err
	}
	return string(index.Digest), nil
}
