package oci

import (
	"fmt"
	"strings"
)

// maxTag is the length limit the distribution specification puts on a tag.
const maxTag = 128

// Tag converts a semantic version into a valid image tag.
//
// The two notations disagree on exactly one character: "+" separates build
// metadata in a version and is illegal in a tag. Every other container tool
// substitutes "_" for it, so a snapshot of 0.1.0-next+9f2ab1c is published as
// 0.1.0-next_9f2ab1c, and someone reading the tag can still see what it was.
func Tag(version string) (string, error) {
	tag := strings.ReplaceAll(strings.TrimSpace(version), "+", "_")
	if err := validTag(tag); err != nil {
		return "", fmt.Errorf("oci: %q cannot be an image tag: %w", version, err)
	}
	return tag, nil
}

func validTag(tag string) error {
	switch {
	case tag == "":
		return fmt.Errorf("it is empty")
	case len(tag) > maxTag:
		return fmt.Errorf("it is longer than %d characters", maxTag)
	}

	if first := tag[0]; !alphanumeric(first) && first != '_' {
		return fmt.Errorf("it starts with %q, and a tag must start with a letter, a digit or an underscore",
			string(first))
	}
	for i := 0; i < len(tag); i++ {
		if c := tag[i]; !alphanumeric(c) && c != '_' && c != '.' && c != '-' {
			return fmt.Errorf("it contains %q, which a tag may not", string(c))
		}
	}
	return nil
}

func alphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
