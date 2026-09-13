package oci_test

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

func TestParseReference(t *testing.T) {
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	tests := []struct {
		in       string
		registry string
		repo     string
		tag      string
		digest   string
	}{
		{"ghcr.io/you/tool", "ghcr.io", "you/tool", "", ""},
		{"ghcr.io/you/tool:v1.2.3", "ghcr.io", "you/tool", "v1.2.3", ""},
		{"ghcr.io/you/tool@" + digest, "ghcr.io", "you/tool", "", digest},
		{"gcr.io/distroless/static:nonroot", "gcr.io", "distroless/static", "nonroot", ""},
		// No dot and no colon in the first element, so it is a path, not a host.
		{"you/tool:v1", "docker.io", "you/tool", "v1", ""},
		{"alpine", "docker.io", "library/alpine", "", ""},
		{"localhost:5000/tool:dev", "localhost:5000", "tool", "dev", ""},
	}

	for _, c := range tests {
		got, err := oci.ParseReference(c.in)
		if err != nil {
			t.Errorf("ParseReference(%q): %v", c.in, err)
			continue
		}
		if got.Registry != c.registry || got.Repository != c.repo ||
			got.Tag != c.tag || string(got.Digest) != c.digest {
			t.Errorf("ParseReference(%q) = %+v", c.in, got)
		}
	}
}

func TestParseReferenceRejects(t *testing.T) {
	for _, in := range []string{"", "   ", "ghcr.io/you/tool@sha256:short", "ghcr.io/you/tool@md5:abc", "you/tool:"} {
		if got, err := oci.ParseReference(in); err == nil {
			t.Errorf("ParseReference(%q) = %+v, want an error", in, got)
		}
	}
}

func TestReferenceTargetAndHost(t *testing.T) {
	ref, err := oci.ParseReference("ghcr.io/you/tool")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Target() != "latest" {
		t.Errorf("Target = %q, want latest", ref.Target())
	}
	if ref.APIHost() != "ghcr.io" {
		t.Errorf("APIHost = %q", ref.APIHost())
	}
	if got := ref.WithTag("v2").String(); got != "ghcr.io/you/tool:v2" {
		t.Errorf("WithTag = %q", got)
	}

	// Docker Hub is written one way and answers on another.
	hub, err := oci.ParseReference("alpine:3")
	if err != nil {
		t.Fatal(err)
	}
	if hub.APIHost() != "registry-1.docker.io" {
		t.Errorf("APIHost = %q", hub.APIHost())
	}
	if hub.Target() != "3" {
		t.Errorf("Target = %q", hub.Target())
	}
}

func TestTag(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3":              "1.2.3",
		"1.2.3-rc.1":         "1.2.3-rc.1",
		"0.1.0-next+9f2ab1c": "0.1.0-next_9f2ab1c",
		"1.0.0+build.1":      "1.0.0_build.1",
	} {
		got, err := oci.Tag(in)
		if err != nil {
			t.Errorf("Tag(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Tag(%q) = %q, want %q", in, got, want)
		}
	}

	for _, in := range []string{"", "   ", "-leading", ".dot", "has space", "has/slash", strings.Repeat("x", 129)} {
		if got, err := oci.Tag(in); err == nil {
			t.Errorf("Tag(%q) = %q, want an error", in, got)
		}
	}
}
