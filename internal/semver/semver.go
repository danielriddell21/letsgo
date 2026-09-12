// Package semver compares version tags.
//
// Only what a release tool needs: parse a tag, order two versions, and say
// whether one is a pre-release. Ordering matters because "the previous
// release" cannot be answered by asking a forge for a list of tags — the API
// returns them in an order of its own, and lexical comparison puts v0.10.0
// before v0.9.0.
package semver

import (
	"strconv"
	"strings"
)

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Prerelease          string
	Build               string
}

// IsPrerelease reports whether the version carries a pre-release segment.
func (v Version) IsPrerelease() bool { return v.Prerelease != "" }

// Parse reads a version, with or without a leading "v".
func Parse(tag string) (Version, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if s == "" {
		return Version{}, false
	}

	var v Version
	if i := strings.IndexByte(s, '+'); i >= 0 {
		v.Build, s = s[i+1:], s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.Prerelease, s = s[i+1:], s[:i]
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}

	var err error
	if v.Major, err = atoi(parts[0]); err != nil {
		return Version{}, false
	}
	if v.Minor, err = atoi(parts[1]); err != nil {
		return Version{}, false
	}
	if v.Patch, err = atoi(parts[2]); err != nil {
		return Version{}, false
	}
	return v, true
}

// atoi rejects the forms strconv accepts but semver does not: signs, and
// leading zeroes.
func atoi(s string) (int, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, strconv.ErrSyntax
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	return strconv.Atoi(s)
}

// Compare orders two versions: -1 if a sorts before b, 0 if equal, 1 if after.
// Build metadata is ignored, as the specification requires.
func Compare(a, b Version) int {
	for _, pair := range [][2]int{
		{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch},
	} {
		if pair[0] != pair[1] {
			return sign(pair[0] - pair[1])
		}
	}

	// A version with a pre-release sorts before the release it precedes.
	switch {
	case a.Prerelease == "" && b.Prerelease == "":
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	}
	return comparePrerelease(a.Prerelease, b.Prerelease)
}

// comparePrerelease compares dot-separated identifiers: numeric ones
// numerically, others lexically, and numeric sorts before non-numeric.
func comparePrerelease(a, b string) int {
	aParts, bParts := strings.Split(a, "."), strings.Split(b, ".")

	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		x, y := aParts[i], bParts[i]
		if x == y {
			continue
		}

		xn, xErr := atoi(x)
		yn, yErr := atoi(y)
		switch {
		case xErr == nil && yErr == nil:
			return sign(xn - yn)
		case xErr == nil:
			return -1
		case yErr == nil:
			return 1
		default:
			return strings.Compare(x, y)
		}
	}
	// A longer set of identifiers sorts after a shorter identical prefix.
	return sign(len(aParts) - len(bParts))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// Latest returns the highest version among tags, ignoring any that do not
// parse and any listed in exclude. It returns "" when there is none.
func Latest(tags []string, exclude ...string) string {
	skip := make(map[string]bool, len(exclude))
	for _, tag := range exclude {
		skip[tag] = true
	}

	best, bestTag := Version{}, ""
	for _, tag := range tags {
		if skip[tag] {
			continue
		}
		v, ok := Parse(tag)
		if !ok {
			continue
		}
		if bestTag == "" || Compare(v, best) > 0 {
			best, bestTag = v, tag
		}
	}
	return bestTag
}
