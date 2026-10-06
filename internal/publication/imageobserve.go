package publication

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielriddell21/letsgo/internal/release"

	"github.com/danielriddell21/letsgo/internal/oci"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/plan"
)

// ObserveImages says, for every tag PushImages would write, what the registry
// holds now and what the release would leave there.
//
// It only reads. A floating tag that would not move, because what it points
// at is already newer, is left out: nothing is going to happen to it.
func ObserveImages(ctx context.Context, builds []release.ImageBuild, token string) ([]plan.Action, error) {
	return observeImages(ctx, builds, func(host string) *oci.Registry { return registryFor(host, token) })
}

// observeImages is ObserveImages with the registry client for a host
// supplied, so it can be driven against an in-process registry.
func observeImages(ctx context.Context, builds []release.ImageBuild, registryAt func(host string) *oci.Registry) ([]plan.Action, error) {
	var actions []plan.Action

	for _, built := range builds {
		registry := registryAt(built.APIHost)
		planned := string(built.Index.Digest)

		var releasing semver.Version
		if len(built.Floating) > 0 {
			v, ok := semver.Parse(built.Version)
			if !ok {
				return nil, fmt.Errorf("release: %q is not a version floating tags can be compared against", built.Version)
			}
			releasing = v
		}

		for _, tag := range built.Tags {
			action, err := observeTag(ctx, registry, built, tag, planned, nil)
			if err != nil {
				return nil, err
			}
			actions = append(actions, action)
		}
		for _, tag := range built.Floating {
			action, err := observeTag(ctx, registry, built, tag, planned, &releasing)
			if err != nil {
				return nil, err
			}
			if action.Target != "" {
				actions = append(actions, action)
			}
		}
	}
	return actions, nil
}

// observeTag compares one tag's current target with the index being pushed.
// A non-nil releasing marks a floating tag, which only moves forward: where
// it would not, the returned action is empty.
func observeTag(
	ctx context.Context, registry *oci.Registry, built release.ImageBuild, tag, planned string, releasing *semver.Version,
) (plan.Action, error) {
	action := plan.Action{
		Kind:    plan.KindImage,
		Target:  fmt.Sprintf("%s/%s:%s", built.Registry, built.Repository, tag),
		Planned: planned,
	}

	fetched, err := registry.Manifest(ctx, built.Repository, tag)
	if err != nil {
		var apiErr *oci.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			action.Op = plan.Add
			return action, nil
		}
		return plan.Action{}, err
	}

	action.Observed = string(fetched.Digest)
	if action.Observed == planned {
		action.Op = plan.Keep
		return action, nil
	}

	if releasing != nil {
		current, ok := currentVersion(fetched.Content)
		if !ok || semver.Compare(*releasing, current) <= 0 {
			return plan.Action{}, nil
		}
	}
	action.Op = plan.Change
	return action, nil
}
