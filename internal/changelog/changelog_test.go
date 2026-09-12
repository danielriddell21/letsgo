package changelog

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
)

func commit(sha, subject, body, author string) discover.Commit {
	return discover.Commit{SHA: sha, Subject: subject, Body: body, Author: author}
}

func TestParsesConventionalCommits(t *testing.T) {
	cases := []struct {
		subject  string
		wantType string
		wantText string
		scope    string
		breaking bool
	}{
		{"feat: add timeouts", "feat", "add timeouts", "", false},
		{"fix(client): retry on 503", "fix", "retry on 503", "client", false},
		{"feat!: drop v1 API", "feat", "drop v1 API", "", true},
		{"feat(api)!: rename Do", "feat", "rename Do", "api", true},
		{"FIX: capitalised type", "fix", "capitalised type", "", false},
		{"just a plain subject", "", "just a plain subject", "", false},
		{"not:conventional without space", "not", "conventional without space", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			got := parse(commit("abc1234def", tc.subject, "", "Dan"))
			if got.Type != tc.wantType || got.Subject != tc.wantText ||
				got.Scope != tc.scope || got.Breaking != tc.breaking {
				t.Errorf("got %+v, want type=%q subject=%q scope=%q breaking=%v",
					got, tc.wantType, tc.wantText, tc.scope, tc.breaking)
			}
		})
	}
}

// The trailer is the half of the convention people actually use when a change
// needs explaining.
func TestBreakingChangeTrailer(t *testing.T) {
	e := parse(commit("abc1234", "feat: rework config", "BREAKING CHANGE: the old keys are gone", "Dan"))
	if !e.Breaking {
		t.Error("BREAKING CHANGE trailer was not detected")
	}
	e = parse(commit("abc1234", "feat: rework config", "BREAKING-CHANGE: hyphenated form", "Dan"))
	if !e.Breaking {
		t.Error("hyphenated trailer was not detected")
	}
}

func TestPullRequestNumbers(t *testing.T) {
	// Squash merge: GitHub appends the number to the subject.
	e := parse(commit("abc1234", "feat: add timeouts (#123)", "", "Dan"))
	if e.PR != 123 {
		t.Errorf("PR = %d, want 123", e.PR)
	}
	if strings.Contains(e.Subject, "#123") {
		t.Errorf("subject still carries the number: %q", e.Subject)
	}

	// Merge commit: the subject names the PR and the body describes it.
	e = parse(commit("abc1234", "Merge pull request #456 from dan/feature", "feat: add retries", "Dan"))
	if e.PR != 456 {
		t.Errorf("PR = %d, want 456", e.PR)
	}
	if e.Type != "feat" || e.Subject != "add retries" {
		t.Errorf("merge commit body was not used: %+v", e)
	}
}

func TestMaintenanceCommitsAreCountedNotListed(t *testing.T) {
	c := Build("v1.0.0", "v1.1.0", []discover.Commit{
		commit("a1111111", "feat: something useful", "", "Dan"),
		commit("b2222222", "chore: bump deps", "", "Dan"),
		commit("c3333333", "ci: fix workflow", "", "Dan"),
		commit("d4444444", "test: add coverage", "", "Dan"),
	})

	if len(c.Entries) != 1 {
		t.Errorf("listed %d entries, want 1: %+v", len(c.Entries), c.Entries)
	}
	if c.Hidden != 3 {
		t.Errorf("Hidden = %d, want 3", c.Hidden)
	}

	md := c.Markdown()
	// Omitted, but never silently.
	if !strings.Contains(md, "3 maintenance commits not shown") {
		t.Errorf("omission is not disclosed:\n%s", md)
	}
	if strings.Contains(md, "bump deps") {
		t.Errorf("maintenance commit was listed:\n%s", md)
	}
}

// A breaking maintenance commit is still breaking.
func TestBreakingMaintenanceIsNotHidden(t *testing.T) {
	c := Build("", "v2.0.0", []discover.Commit{
		commit("a1111111", "build!: require Go 1.24", "", "Dan"),
	})
	if c.Hidden != 0 || len(c.Entries) != 1 {
		t.Errorf("a breaking change was hidden: entries=%d hidden=%d", len(c.Entries), c.Hidden)
	}
}

func TestMarkdownOrdersSections(t *testing.T) {
	c := Build("v1.0.0", "v2.0.0", []discover.Commit{
		commit("a1111111", "docs: tidy readme", "", "Dan"),
		commit("b2222222", "fix: handle nil", "", "Ada"),
		commit("c3333333", "feat: add retries", "", "Dan"),
		commit("d4444444", "feat!: remove Do", "", "Ada"),
		commit("e5555555", "something unlabelled", "", "Dan"),
	})

	md := c.Markdown()

	order := []string{"### Breaking changes", "### Features", "### Fixes", "### Documentation", "### Other changes"}
	last := -1
	for _, heading := range order {
		i := strings.Index(md, heading)
		if i < 0 {
			t.Fatalf("%q is missing:\n%s", heading, md)
		}
		if i < last {
			t.Errorf("%q appears out of order:\n%s", heading, md)
		}
		last = i
	}

	// A breaking change belongs in exactly one place, and it is the top.
	if strings.Count(md, "remove Do") != 1 {
		t.Errorf("breaking change listed more than once:\n%s", md)
	}
	if !strings.Contains(md, "### Contributors\n\nAda, Dan") {
		t.Errorf("contributors missing or unsorted:\n%s", md)
	}
}

// A single-author release does not need to be told who wrote it.
func TestSingleContributorIsNotListed(t *testing.T) {
	c := Build("", "v1.0.0", []discover.Commit{commit("a1111111", "feat: first", "", "Dan")})
	if strings.Contains(c.Markdown(), "Contributors") {
		t.Errorf("contributors listed for a single author:\n%s", c.Markdown())
	}
}

func TestEntryReferences(t *testing.T) {
	c := Build("", "v1.0.0", []discover.Commit{
		commit("abc1234def5678", "feat: with a pr (#7)", "", "Dan"),
		commit("fed4321cba8765", "feat: without one", "", "Dan"),
	})
	md := c.Markdown()

	if !strings.Contains(md, "with a pr (#7)") {
		t.Errorf("pull request reference missing:\n%s", md)
	}
	// Falling back to the abbreviated sha keeps every entry traceable.
	if !strings.Contains(md, "without one (fed4321)") {
		t.Errorf("sha fallback missing:\n%s", md)
	}
}

func TestScopeIsHighlighted(t *testing.T) {
	c := Build("", "v1.0.0", []discover.Commit{commit("a1111111", "fix(archive): zero the gzip mtime", "", "Dan")})
	if !strings.Contains(c.Markdown(), "**archive:** zero the gzip mtime") {
		t.Errorf("scope not rendered:\n%s", c.Markdown())
	}
}

func TestEmptyRange(t *testing.T) {
	if got := Build("v1.0.0", "v1.0.1", nil).Markdown(); got != "No changes.\n" {
		t.Errorf("Markdown() = %q", got)
	}
}

// The API diff describes what the code did; the commit messages describe what
// someone meant. For a library the first is what a reader is deciding on.
func TestAPIChangesSection(t *testing.T) {
	c := Build("v1.0.0", "v2.0.0", []discover.Commit{
		commit("a1111111", "feat: rework the client", "", "Dan"),
	}).WithAPIChanges([]gate.Change{
		{Package: "example.com/foo", Kind: gate.Compatible, Text: "WithTimeout: added"},
		{Package: "example.com/foo", Kind: gate.Incompatible, Text: "(*Client).Do: removed"},
	})

	md := c.Markdown()

	if !strings.Contains(md, "### API changes") {
		t.Fatalf("no API section:\n%s", md)
	}
	// Breaking first: it is the entry that can cost the reader an afternoon.
	breaking := strings.Index(md, "(*Client).Do: removed")
	added := strings.Index(md, "WithTimeout: added")
	if breaking < 0 || added < 0 || breaking > added {
		t.Errorf("incompatible changes are not listed first:\n%s", md)
	}
	if !strings.Contains(md, "`!`") || !strings.Contains(md, "`+`") {
		t.Errorf("changes are not marked by consequence:\n%s", md)
	}
}

func TestNoAPISectionWithoutChanges(t *testing.T) {
	md := Build("", "v1.0.0", []discover.Commit{commit("a1111111", "feat: first", "", "Dan")}).Markdown()
	if strings.Contains(md, "API changes") {
		t.Errorf("an empty API section was rendered:\n%s", md)
	}
}
