// Package receipt renders a verification as a till receipt.
//
// It is a joke that is still a faithful report: it draws the same
// verify.Result as the ordinary output, never a check of its own, so it cannot
// say ✓ where verify says ✗. Every line maps to something verify established,
// and a check that did not run is printed as skipped rather than left out.
package receipt

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielriddell21/letsgo/internal/verify"
)

const (
	// width is the receipt's fixed width in display columns. Nothing wraps:
	// what would not fit is shortened.
	width = 40

	shop = "SHIP IT & SAVE"
)

// Render draws r as a receipt printed at now.
//
// The time is the moment of printing, not anything about the release, so it is
// an argument rather than something the receipt looks up.
func Render(r *verify.Result, now time.Time) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }

	inner := strings.Repeat("═", width-2)
	line("╔" + inner + "╗")
	line("║" + padRight(centre(shop, width-2), width-2) + "║")
	line("╚" + inner + "╝")

	goVersion := ""
	if r.Manifest != nil {
		goVersion = r.Manifest.Builder.Go
	}
	line(pair(r.Repo, r.Tag))
	line(pair(now.Format("2006-01-02 15:04"), goVersion))
	line(rule())

	differing := 0
	for _, a := range r.Artifacts {
		if a.Status == verify.Fail {
			differing++
		}
		line(shorten(a.Name, width))
		line(pair("  "+size(a.Size)+"  "+digest(a.SHA256), itemStatus(a.Status)))
	}

	line(rule())
	line(pair("ITEMS", fmt.Sprint(len(r.Artifacts))))
	line(pair("BYTES DIFFERING", fmt.Sprint(differing)))
	line(pair("PROVENANCE", checkLabel(r, "provenance", "ATTESTED", "NOT ATTESTED")))
	if _, ok := find(r, "images"); ok {
		line(pair("IMAGES", checkLabel(r, "images", "MATCH", "UNCHECKED")))
	}

	line(rule())
	if r.OK() {
		line(pair("TOTAL", "BIT-FOR-BIT ✓"))
		line("odds of an accidental match: 1 in 2²⁵⁶")
	} else {
		line(pair("TOTAL", "VOID"))
		const stamp = "VOID — DO NOT ACCEPT"
		box := strings.Repeat("─", utf8.RuneCountInString(stamp)+4)
		line(centre("┌"+box+"┐", width))
		line(centre("│  "+stamp+"  │", width))
		line(centre("└"+box+"┘", width))
	}

	b.WriteString("\n")
	line(centre("thank you for verifying. come again", width))
	return b.String()
}

// itemStatus is how an artifact's outcome reads on the receipt.
func itemStatus(s verify.Status) string {
	switch s {
	case verify.Pass:
		return "✓ MATCH"
	case verify.Fail:
		return "✗ MISMATCH"
	default:
		return "— SKIPPED"
	}
}

// checkLabel words a whole check's outcome, or says it was skipped when the
// check never ran.
func checkLabel(r *verify.Result, name, pass, warn string) string {
	c, ok := find(r, name)
	if !ok {
		return "— SKIPPED"
	}
	switch c.Status {
	case verify.Pass:
		return "✓ " + pass
	case verify.Fail:
		return "✗ FAILED"
	case verify.Warn:
		return "! " + warn
	default:
		return "— SKIPPED"
	}
}

func find(r *verify.Result, name string) (verify.Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return verify.Check{}, false
}

func rule() string { return strings.Repeat("-", width) }

// pair puts left at the left edge and right at the right, shortening either
// if the two would not otherwise fit. Neither takes more than half the width
// on its own, so a long tag cannot push the repository off the line.
func pair(left, right string) string {
	right = shorten(right, width/2)
	room := width - utf8.RuneCountInString(right) - 1
	left = shorten(left, room)
	gap := width - utf8.RuneCountInString(left) - utf8.RuneCountInString(right)
	return left + strings.Repeat(" ", gap) + right
}

func centre(s string, within int) string {
	pad := within - utf8.RuneCountInString(s)
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad/2) + s
}

// shorten cuts s down to n columns from the middle, so both the start of a
// name and its distinguishing tail survive.
func shorten(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:max(n, 0)])
	}
	keep := n - 1
	head := (keep + 1) / 2
	return string(runes[:head]) + "…" + string(runes[len(runes)-(keep-head):])
}

// digest abbreviates a sha256 to its head and tail.
func digest(sum string) string {
	if len(sum) <= 13 {
		return sum
	}
	return sum[:8] + "…" + sum[len(sum)-4:]
}

// size formats a byte count in decimal units, the way a file listing does.
func size(n int64) string {
	switch {
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1f kB", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// padRight fills s with spaces to n columns.
func padRight(s string, n int) string {
	return s + strings.Repeat(" ", max(n-utf8.RuneCountInString(s), 0))
}
