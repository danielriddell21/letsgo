package apply

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

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// maxDifferences caps how many differing fields a refusal lists.
const maxDifferences = 20

// Agreed is the check an apply makes once it has rebuilt, and before it
// publishes anything: that this is the release the plan was made for, and that
// the rebuild reproduced what the plan predicted.
func Agreed(file *plandiff.File) func(*plan.Plan, *release.Result) error {
	return func(p *plan.Plan, result *release.Result) error {
		if p.Tag != file.Tag {
			return fmt.Errorf("letsgo: the plan is for %s, and HEAD is tagged %s", file.Tag, p.Tag)
		}
		if p.Git.Commit != file.Commit {
			return fmt.Errorf("letsgo: the plan is for %s at %s, and %s is now at %s",
				file.Tag, Short12(file.Commit), p.Tag, Short12(p.Git.Commit))
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

// Fresh reads the forge as it is now and holds it to the plan: every
// target must be where the plan found it or where it would leave it, or the
// plan is stale and nothing is written. What it returns is the set of writes the
// apply may make.
func Fresh(ctx context.Context, file *plandiff.File, t publication.Options, logf func(string, ...any)) (*Writes, error) {
	current, err := publication.Observe(ctx, t)
	if err != nil {
		return nil, err
	}
	if err := Stale(file.Actions, current, "letsgo plan -out"); err != nil {
		return nil, err
	}
	for _, name := range AlreadyDone(file.Actions, current) {
		logf("  = %s is already as planned; skipped", name)
	}
	return NewWrites(file.Actions), nil
}

// Guard holds the forge to the plan and returns the clients an apply
// publishes through, which refuse anything the plan did not list. A release
// with no plan publishes through the clients as they are.
func Guard(ctx context.Context, file *plandiff.File, t publication.Options, logf func(string, ...any)) (publish.Forge, brew.FileAPI, error) {
	if file == nil {
		return t.Forge, t.Tap, nil
	}
	writes, err := Fresh(ctx, file, t, logf)
	if err != nil {
		return nil, nil, err
	}
	return writes.Forge(t.Forge, publication.Tag(t.Plan)), writes.Tap(t.Tap), nil
}

// Stale is the error for a plan whose targets have changed since it was
// made, naming each one.
func Stale(saved, current []plandiff.Action, remake string) error {
	drifted := plandiff.Drifted(saved, current)
	if len(drifted) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "letsgo: the plan is stale: %d target(s) changed since it was made; nothing was published", len(drifted))
	for _, d := range drifted {
		b.WriteString("\n    " + d.String())
	}
	b.WriteString("\n  make a new plan with `" + remake + "`")
	return errors.New(b.String())
}

// AlreadyDone names the targets the plan would change that an earlier apply
// has already brought to their planned state.
func AlreadyDone(saved, current []plandiff.Action) []string {
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

// Hold stops an apply whose rebuild is not the release its plan agreed.
// A release with no plan has nothing to hold it to.
func Hold(file *plandiff.File, p *plan.Plan, result *release.Result, logf func(string, ...any)) error {
	if file == nil {
		return nil
	}
	if err := Agreed(file)(p, result); err != nil {
		return err
	}
	logf("  the rebuild matches the plan")
	return nil
}

// HoldAndStamp holds the rebuild to the plan, then records the plan in it.
func HoldAndStamp(file *plandiff.File, p *plan.Plan, result *release.Result, logf func(string, ...any)) (*plandiff.File, error) {
	if err := Hold(file, p, result, logf); err != nil {
		return nil, err
	}
	return Stamp(file, result)
}

// Stamp records the plan in the release an apply is about to publish,
// and returns the plan with the asset that adds to what it lists.
//
// It runs after the rebuild has been held to the plan, which predicts the
// manifest without this record. A plan that does not write the manifest —
// one already published — leaves it as it is: the record would be a change the
// plan never listed.
func Stamp(file *plandiff.File, result *release.Result) (*plandiff.File, error) {
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

// Short12 is the first 12 characters of a digest or commit, enough to tell
// two apart.
func Short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
