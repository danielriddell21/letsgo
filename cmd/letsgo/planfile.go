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

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
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

// freshAgainst reads the forge as it is now and holds it to the plan: every
// target must be where the plan found it or where it would leave it, or the
// plan is stale and nothing is written. What it returns is the set of writes the
// apply may make.
func freshAgainst(ctx context.Context, p *plan.Plan, file *plandiff.File, t forgeTargets) (*plannedWrites, error) {
	current, err := diffForge(ctx, p, t)
	if err != nil {
		return nil, err
	}
	if err := staleness(file.Actions, current); err != nil {
		return nil, err
	}
	for _, name := range alreadyDone(file.Actions, current) {
		fmt.Printf("  = %s is already as planned; skipped\n", name)
	}
	return newPlannedWrites(file.Actions), nil
}

// guardApply holds the forge to the plan and returns the clients an apply
// publishes through, which refuse anything the plan did not list. A release
// with no plan publishes through the clients as they are.
func guardApply(ctx context.Context, p *plan.Plan, file *plandiff.File, t forgeTargets) (publish.Forge, brew.FileAPI, error) {
	if file == nil {
		return t.Forge, t.Tap, nil
	}
	writes, err := freshAgainst(ctx, p, file, t)
	if err != nil {
		return nil, nil, err
	}
	return guardedForge{Forge: t.Forge, tag: releaseTag(p), writes: writes},
		guardedTap{FileAPI: t.Tap, writes: writes}, nil
}

// staleness is the error for a plan whose targets have changed since it was
// made, naming each one.
func staleness(saved, current []plandiff.Action) error {
	drifted := plandiff.Drifted(saved, current)
	if len(drifted) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "letsgo: the plan is stale: %d target(s) changed since it was made; nothing was published", len(drifted))
	for _, d := range drifted {
		b.WriteString("\n    " + d.String())
	}
	b.WriteString("\n  make a new plan with `letsgo plan -out`")
	return errors.New(b.String())
}

// alreadyDone names the targets the plan would change that an earlier apply
// has already brought to their planned state.
func alreadyDone(saved, current []plandiff.Action) []string {
	now := make(map[string]plandiff.Action, len(current))
	for _, a := range current {
		now[string(a.Kind)+"\x00"+a.Target] = a
	}
	var out []string
	for _, a := range saved {
		if a.Op == plandiff.Keep {
			continue
		}
		if c, ok := now[string(a.Kind)+"\x00"+a.Target]; ok && c.Op == plandiff.Keep {
			out = append(out, string(a.Kind)+" "+a.Target)
		}
	}
	return out
}

// holdToPlan stops an apply whose rebuild is not the release its plan agreed.
// A release with no plan has nothing to hold it to.
func holdToPlan(file *plandiff.File, p *plan.Plan, result *release.Result) error {
	if file == nil {
		return nil
	}
	if err := agreedPlan(file)(p, result); err != nil {
		return err
	}
	fmt.Println("  the rebuild matches the plan")
	return nil
}

// holdAndStamp holds the rebuild to the plan, then records the plan in it.
func holdAndStamp(file *plandiff.File, p *plan.Plan, result *release.Result) (*plandiff.File, error) {
	if err := holdToPlan(file, p, result); err != nil {
		return nil, err
	}
	return stampApplied(file, result)
}

// stampApplied records the plan in the release an apply is about to publish,
// and returns the plan with the asset that adds to what it lists.
//
// It runs after the rebuild has been held to the plan, which predicts the
// manifest without this record. A plan that does not write the manifest —
// one already published — leaves it as it is: the record would be a change the
// plan never listed.
func stampApplied(file *plandiff.File, result *release.Result) (*plandiff.File, error) {
	if file == nil || !writesManifest(file.Actions) {
		return file, nil
	}
	data, err := file.Encode()
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	if err := release.StampPlan(result, data, file.CreatedAt, file.LetsgoVersion); err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}

	stamped := *file
	stamped.Actions = append(append([]plandiff.Action(nil), file.Actions...), plandiff.Action{
		Kind: plandiff.KindAsset, Target: release.PlanFileName, Op: plandiff.Add,
		Planned: "sha256:" + result.Manifest.Plan.SHA256,
	})
	return &stamped, nil
}

func writesManifest(actions []plandiff.Action) bool {
	for _, a := range actions {
		if a.Kind == plandiff.KindAsset && a.Target == manifest.FileName && a.Op != plandiff.Keep {
			return true
		}
	}
	return false
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
