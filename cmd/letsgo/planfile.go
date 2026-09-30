package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// errPlanChanges marks a plan that would change something, for
// `plan --exit-code`: the plan was read fine, and the exit status is the
// answer.
var errPlanChanges = errors.New("the plan has changes")

// maxDifferences caps how many differing fields a refusal lists.
const maxDifferences = 20

// savePlan writes what a diff found as a plan file, and returns its digest.
func savePlan(p *plan.Plan, d *forgeDiff, path string) (string, error) {
	file := &plandiff.File{
		Schema:         plandiff.FileSchema,
		LetsgoVersion:  version,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		Kind:           plandiff.FileKindRelease,
		Repo:           p.Repo.Owner + "/" + p.Repo.Name,
		Tag:            p.Tag,
		Commit:         p.Git.Commit,
		ManifestSHA256: d.ManifestSHA256,
		Manifest:       json.RawMessage(d.Manifest),
		Actions:        d.Actions,
	}
	if err := file.Write(path); err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	digest, err := file.Digest()
	if err != nil {
		return "", fmt.Errorf("letsgo: %w", err)
	}
	return digest, nil
}

// diffRun is what `letsgo plan --diff` and `-out` are asked to do.
type diffRun struct {
	Tokens   diffTokens
	Out      string
	ExitCode bool
	Started  time.Time
}

// diffAndSave prints what a release would change, saves the plan when asked,
// and, for --exit-code, reports a plan that has changes through the exit
// status.
func diffAndSave(ctx context.Context, p *plan.Plan, r diffRun) error {
	d, err := planDiff(ctx, p, r.Tokens)
	if err != nil {
		return err
	}
	return finishDiff(p, d, r)
}

// finishDiff is everything after the forge has been read: show the actions,
// save the plan, and answer --exit-code.
func finishDiff(p *plan.Plan, d *forgeDiff, r diffRun) error {
	fmt.Println()
	fmt.Print(plandiff.Render(d.Actions))

	if r.Out != "" {
		digest, err := savePlan(p, d, r.Out)
		if err != nil {
			return err
		}
		fmt.Printf("\n  saved to %s (%s)\n  apply it with: letsgo apply %s\n", r.Out, digest, r.Out)
	}

	fmt.Printf("\n  plan ok in %s", took(r.Started))
	switch {
	case r.Out != "":
		fmt.Println()
	case plandiff.HasChanges(d.Actions):
		fmt.Println(" · run `letsgo release` to apply it")
	default:
		fmt.Println(" · nothing to change")
	}

	if r.ExitCode && plandiff.HasChanges(d.Actions) {
		return errPlanChanges
	}
	return nil
}

// agreedPlan is the check an apply makes once it has rebuilt, and before it
// publishes anything: that this is the release the plan was made for, and that
// the rebuild reproduced what the plan predicted.
func agreedPlan(file *plandiff.File) func(*plan.Plan, *release.Result) error {
	return func(p *plan.Plan, result *release.Result) error {
		if p.Tag != file.Tag {
			return fmt.Errorf("letsgo: the plan is for %s, and HEAD is tagged %s", file.Tag, p.Tag)
		}
		if p.Git.Commit != file.Commit {
			return fmt.Errorf("letsgo: the plan is for %s at %s, and %s is now at %s",
				file.Tag, short12(file.Commit), p.Tag, short12(p.Git.Commit))
		}

		rebuilt, err := os.ReadFile(filepath.Join(result.Dir, manifest.FileName))
		if err != nil {
			return fmt.Errorf("letsgo: %w", err)
		}
		sum := sha256.Sum256(rebuilt)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != file.ManifestSHA256 {
			return refusal(file, rebuilt, got)
		}
		return nil
	}
}

// refusal is the error for a rebuild that did not reproduce the plan, naming
// the fields that differ.
func refusal(file *plandiff.File, rebuilt []byte, got string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "letsgo: the rebuild does not match the plan (%s, planned %s); nothing was published",
		got, file.ManifestSHA256)
	for _, d := range differences(file.Manifest, rebuilt) {
		b.WriteString("\n    " + d)
	}
	return errors.New(b.String())
}

// differences lists the fields that differ between two JSON documents, as
// "path: planned → rebuilt", in path order.
func differences(planned, rebuilt []byte) []string {
	a, errA := flatten(planned)
	b, errB := flatten(rebuilt)
	if errA != nil || errB != nil {
		return nil
	}

	keys := make(map[string]bool, len(a)+len(b))
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var out []string
	for _, k := range ordered {
		if a[k] == b[k] {
			continue
		}
		if len(out) == maxDifferences {
			out = append(out, "…")
			break
		}
		out = append(out, fmt.Sprintf("%s: %s → %s", k, orAbsent(a, k), orAbsent(b, k)))
	}
	return out
}

func orAbsent(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return "absent"
}

// flatten turns a JSON document into path → value, so two can be compared
// field by field without knowing their schema.
func flatten(data []byte) (map[string]string, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	out := map[string]string{}
	walk("", doc, out)
	return out, nil
}

func walk(path string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			walk(join(path, k), child, out)
		}
	case []any:
		for i, child := range t {
			walk(fmt.Sprintf("%s[%d]", path, i), child, out)
		}
	default:
		out[path] = fmt.Sprint(t)
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
