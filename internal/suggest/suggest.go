// Package suggest finds the name a mistyped one probably meant, for a
// did-you-mean in an error message. It is a leaf package, so config, discover
// and anything else that reports an unknown name share one rule.
package suggest

import "strings"

// Nearest returns the candidate want most likely meant, or "" when none is
// close enough to be worth saying. A guess that is as likely wrong as right
// serves the reader worse than the full list of candidates does.
//
// A candidate that differs from want only in case wins outright, then one
// that want is a prefix of; otherwise the closest by edit distance wins, if
// it is within a budget that grows with the length of want. A candidate equal
// to want is never suggested: the caller has found it unknown for some other
// reason.
func Nearest(want string, candidates []string) string {
	lower := strings.ToLower(want)

	for _, c := range candidates {
		if c != want && strings.EqualFold(c, want) {
			return c
		}
	}
	if want != "" {
		for _, c := range candidates {
			if c != want && strings.HasPrefix(strings.ToLower(c), lower) {
				return c
			}
		}
	}

	budget := budgetFor(len([]rune(want)))
	best, bestDist := "", budget+1
	for _, c := range candidates {
		if c == want {
			continue
		}
		if d := distance(lower, strings.ToLower(c)); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// budgetFor is how many edits away a suggestion may be: one for a short word,
// where a second edit would rewrite it, and more as the word grows.
func budgetFor(length int) int {
	switch {
	case length < 5:
		return 1
	case length < 9:
		return 2
	default:
		return 3
	}
}

// distance is the optimal string alignment distance between a and b: the
// fewest insertions, deletions, substitutions and swaps of two adjacent
// letters that turn one into the other. Counting a swap as one edit is what
// lets a transposed pair (sbmo for sbom) still earn a suggestion.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}

	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
