// Package changelog turns a range of commits into release notes.
//
// The parsing here is deliberately separate from reading git, so that what a
// commit message means is decided by pure functions over plain data rather
// than by something that also has to run a subprocess.
package changelog

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
)

// Entry is one change worth listing.
type Entry struct {
	SHA      string
	Subject  string
	Type     string
	Scope    string
	Breaking bool
	PR       int
	Author   string
}

// Changelog is a rendered set of release notes.
type Changelog struct {
	From         string
	To           string
	Entries      []Entry
	Hidden       int
	Contributors []string

	// APIChanges is the exported API delta, rendered as its own section.
	APIChanges []gate.Change
}

// WithAPIChanges attaches an exported API delta to the notes.
//
// A commit message is a lossy, optional account of a change, written by
// someone who already knew what they meant. The API diff is the change. For a
// library it is also the part a reader is deciding on: whether upgrading will
// cost them anything.
func (c *Changelog) WithAPIChanges(changes []gate.Change) *Changelog {
	c.APIChanges = changes
	return c
}

// conventional matches "type(scope)!: subject".
var conventional = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^)]*)\))?(!)?:\s*(.+)$`)

// Two shapes carry a pull request number: the merge commit git writes, and the
// "(#123)" suffix a squash merge appends to the subject.
var (
	mergeSubject = regexp.MustCompile(`^Merge pull request #(\d+) from \S+`)
	squashSuffix = regexp.MustCompile(`\s*\(#(\d+)\)$`)
)

// maintenance types are omitted from release notes by default. They are real
// work, but a person reading notes to decide whether to upgrade does not need
// them. The count is reported rather than the omission hidden.
var maintenance = map[string]bool{
	"chore": true, "ci": true, "test": true, "style": true, "build": true,
}

// headings maps a conventional-commit type to its section, in the order the
// sections appear.
var sections = []struct {
	Types   []string
	Heading string
}{
	{[]string{"feat"}, "Features"},
	{[]string{"fix"}, "Fixes"},
	{[]string{"perf"}, "Performance"},
	{[]string{"refactor"}, "Refactoring"},
	{[]string{"docs"}, "Documentation"},
	{[]string{"revert"}, "Reverts"},
}

// Build turns commits into a changelog.
func Build(from, to string, commits []discover.Commit) *Changelog {
	c := &Changelog{From: from, To: to}

	authors := map[string]bool{}
	for _, commit := range commits {
		entry := parse(commit)

		authors[entry.Author] = true

		if maintenance[entry.Type] && !entry.Breaking {
			c.Hidden++
			continue
		}
		c.Entries = append(c.Entries, entry)
	}

	for author := range authors {
		if author != "" {
			c.Contributors = append(c.Contributors, author)
		}
	}
	sort.Strings(c.Contributors)

	return c
}

func parse(commit discover.Commit) Entry {
	entry := Entry{SHA: commit.SHA, Subject: commit.Subject, Author: commit.Author}

	// A merge commit's subject names the pull request; the change itself is
	// described in the body.
	if m := mergeSubject.FindStringSubmatch(commit.Subject); m != nil {
		entry.PR, _ = strconv.Atoi(m[1])
		if first, _, _ := strings.Cut(commit.Body, "\n"); strings.TrimSpace(first) != "" {
			entry.Subject = strings.TrimSpace(first)
		}
	}

	if m := squashSuffix.FindStringSubmatch(entry.Subject); m != nil {
		entry.PR, _ = strconv.Atoi(m[1])
		entry.Subject = strings.TrimSpace(squashSuffix.ReplaceAllString(entry.Subject, ""))
	}

	if m := conventional.FindStringSubmatch(entry.Subject); m != nil {
		entry.Type = strings.ToLower(m[1])
		entry.Scope = m[2]
		entry.Breaking = m[3] == "!"
		entry.Subject = strings.TrimSpace(m[4])
	}

	// The trailer is the other half of the convention, and the half people
	// actually use when the change needs explaining.
	for _, line := range strings.Split(commit.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BREAKING CHANGE:") || strings.HasPrefix(line, "BREAKING-CHANGE:") {
			entry.Breaking = true
			break
		}
	}

	return entry
}

// Markdown renders the release notes.
func (c *Changelog) Markdown() string {
	if len(c.Entries) == 0 && c.Hidden == 0 {
		return "No changes.\n"
	}

	var b strings.Builder
	used := map[int]bool{}

	// Breaking changes come first regardless of type: they are the only thing
	// in a changelog that can cost the reader an afternoon.
	c.writeSection(&b, used, "Breaking changes", c.pick(used, func(e Entry) bool {
		return e.Breaking
	}))

	for _, section := range sections {
		c.writeSection(&b, used, section.Heading, c.pick(used, func(e Entry) bool {
			return slices.Contains(section.Types, e.Type)
		}))
	}

	// Anything conventionally labelled but not in a section above, plus
	// anything that carried no label at all.
	c.writeSection(&b, used, "Other changes", c.pick(used, func(Entry) bool { return true }))

	writeAPIChanges(&b, c.APIChanges)

	if c.Hidden > 0 {
		fmt.Fprintf(&b, "_%s not shown._\n\n", plural(c.Hidden, "maintenance commit"))
	}

	if len(c.Contributors) > 1 {
		fmt.Fprintf(&b, "### Contributors\n\n%s\n\n", strings.Join(c.Contributors, ", "))
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// writeAPIChanges renders the exported API delta, breaking changes first.
func writeAPIChanges(b *strings.Builder, changes []gate.Change) {
	if len(changes) == 0 {
		return
	}

	b.WriteString("### API changes\n\n")
	for _, kind := range []gate.ChangeKind{gate.Incompatible, gate.Compatible} {
		for _, c := range changes {
			if c.Kind != kind {
				continue
			}
			// The marker carries the consequence: one of these costs the
			// reader work, the other does not.
			marker := "+"
			if kind == gate.Incompatible {
				marker = "!"
			}
			fmt.Fprintf(b, "- `%s` %s\n", marker, c.String())
		}
	}
	b.WriteString("\n")
}

// pick selects the entries matching want that no earlier section claimed.
func (c *Changelog) pick(used map[int]bool, want func(Entry) bool) []int {
	var indices []int
	for i, e := range c.Entries {
		if !used[i] && want(e) {
			indices = append(indices, i)
		}
	}
	return indices
}

// writeSection writes one heading and its entries, marking them used so a
// commit appears once rather than under every heading it could fit.
func (c *Changelog) writeSection(b *strings.Builder, used map[int]bool, heading string, indices []int) {
	if len(indices) == 0 {
		return
	}

	fmt.Fprintf(b, "### %s\n\n", heading)
	for _, i := range indices {
		writeEntry(b, c.Entries[i])
		used[i] = true
	}
	b.WriteString("\n")
}

func writeEntry(b *strings.Builder, e Entry) {
	b.WriteString("- ")
	if e.Scope != "" {
		fmt.Fprintf(b, "**%s:** ", e.Scope)
	}
	b.WriteString(e.Subject)
	if e.PR > 0 {
		fmt.Fprintf(b, " (#%d)", e.PR)
	} else if len(e.SHA) >= 7 {
		fmt.Fprintf(b, " (%s)", e.SHA[:7])
	}
	b.WriteString("\n")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
