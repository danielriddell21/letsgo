package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/plan"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// errPlanChanges marks a plan that would change something, for
// `plan --exit-code`: the plan was read fine, and the exit status is the
// answer.
var errPlanChanges = errors.New("the plan has changes")

// diffRun is what `letsgo plan --diff` and `-out` are asked to do.
type diffRun struct {
	Out      string
	ExitCode bool
	Started  time.Time

	// Then is what to run to make the plan happen when it is not saved.
	Then string

	// Markdown, when set, is where the plan is written as Markdown, under
	// Title. The text rendering is left out, since the same actions would
	// appear twice.
	Markdown io.Writer
	Title    string
}

// markdownFormat reads --format, reporting whether it asked for Markdown.
func markdownFormat(name string, jsonOutput bool) (bool, error) {
	switch name {
	case "text":
		return false, nil
	case "md":
		if jsonOutput {
			return false, errors.New("letsgo: --format md and --json are two answers to one question; pick one")
		}
		return true, nil
	}
	return false, fmt.Errorf("letsgo: unknown --format %q (text or md)", name)
}

// toMarkdown makes the run write Markdown to standard output, and returns what
// puts standard output back. A summary is piped into a file, so everything
// else the plan says, the gate report and progress, moves to standard error
// where it stays in the log without being mixed into the summary.
func (r *diffRun) toMarkdown(on bool) func() {
	if !on {
		return func() {}
	}
	out := os.Stdout
	os.Stdout = os.Stderr
	r.Markdown = out
	return func() { os.Stdout = out }
}

// finishDiff is everything after the forge has been read: show the actions,
// save the plan, and answer --exit-code.
func finishDiff(p *plan.Plan, d *apply.Diff, r diffRun) error {
	r.Then = "letsgo release"
	r.Title = "letsgo plan"
	return finishPlan(d.Actions, func(path string) (string, error) { return apply.Save(p, d, path, version) }, r)
}

// finishPlan shows a plan's actions, saves it through save when asked, and
// answers --exit-code. It is the part a release plan and a yank plan share.
func finishPlan(actions []plandiff.Action, save func(path string) (string, error), r diffRun) error {
	if r.Markdown != nil {
		if _, err := io.WriteString(r.Markdown, plandiff.Markdown(r.Title, actions)); err != nil {
			return fmt.Errorf("letsgo: writing the summary: %w", err)
		}
	} else {
		fmt.Println()
		fmt.Print(plandiff.Render(actions))
	}

	if r.Out != "" {
		digest, err := save(r.Out)
		if err != nil {
			return err
		}
		fmt.Printf("\n  saved to %s (%s)\n  apply it with: letsgo apply %s\n", r.Out, digest, r.Out)
	}

	fmt.Printf("\n  plan ok in %s", took(r.Started))
	switch {
	case r.Out != "", r.Then == "" && plandiff.HasChanges(actions):
		fmt.Println()
	case plandiff.HasChanges(actions):
		fmt.Printf(" · run `%s` to apply it\n", r.Then)
	default:
		fmt.Println(" · nothing to change")
	}

	if r.ExitCode && plandiff.HasChanges(actions) {
		return errPlanChanges
	}
	return nil
}

// say prints one progress line to standard output.
func say(format string, args ...any) { fmt.Printf(format+"\n", args...) }
