package lsp

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/danielriddell21/letsgo/modsyntax"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// fileKind says which directive set, if any, a config file is decoded
// against. Only letsgo.mod and the global config.mod have one; a plugin's
// own .letsgo/<name>.mod gets syntax and formatting only, per the HLD — its
// directives belong to the plugin, not to core.
type fileKind int

const (
	kindRepo fileKind = iota
	kindGlobal
	kindSyntaxOnly
)

func kindOf(path string) fileKind {
	switch filepath.Base(path) {
	case "letsgo.mod":
		return kindRepo
	case "config.mod":
		return kindGlobal
	default:
		return kindSyntaxOnly
	}
}

// parseDiagnostics runs Parse, and Decode when the file kind has a directive
// set, returning every syntax or decode error as a diagnostic. There is at
// most one: the parser stops at the first error, the same way `letsgo fmt`
// and `letsgo plan` already report it.
func parseDiagnostics(path, text string) []Diagnostic {
	f, err := modsyntax.Parse(path, []byte(text))
	if err != nil {
		return []Diagnostic{diagnosticFromSyntaxError(err)}
	}

	switch kindOf(path) {
	case kindRepo:
		if _, err := config.Decode(f); err != nil {
			return []Diagnostic{diagnosticFromSyntaxError(err)}
		}
	case kindGlobal:
		if _, err := config.DecodeGlobal(f); err != nil {
			return []Diagnostic{diagnosticFromSyntaxError(err)}
		}
	}
	return nil
}

func diagnosticFromSyntaxError(err error) Diagnostic {
	var se *modsyntax.SyntaxError
	if !errors.As(err, &se) {
		return Diagnostic{Message: err.Error(), Severity: SeverityError}
	}
	line := max(se.Pos.Line-1, 0)
	col := max(se.Pos.Col-1, 0)
	d := Diagnostic{
		Range:    Range{Start: Position{Line: line, Character: col}, End: Position{Line: line, Character: col + 1}},
		Severity: SeverityError,
		Message:  se.Msg,
	}
	if se.Suggest != "" {
		d.Range.End.Character = col + len(se.Wrong)
		d.Data = &Suggestion{Wrong: se.Wrong, Suggest: se.Suggest}
	}
	return d
}

// planDiagnostics runs a fast, local plan — no analysis gates, no forge, a
// dirty worktree allowed — and returns a diagnostic for every failing or
// warning check that carries a position. It is only meaningful for letsgo.mod,
// and only once the file parses and decodes cleanly, so the caller skips it
// otherwise.
//
// This is the one place a plan's problems become editor diagnostics (ADR-0008):
// an editor client does not publish them a second time from `letsgo plan
// --json`.
func planDiagnostics(ctx context.Context, path string) []Diagnostic {
	p, err := plan.Resolve(ctx, plan.Options{
		Dir:        filepath.Dir(path),
		Snapshot:   true,
		AllowDirty: true,
	})
	if err != nil {
		// Not every directory holding a letsgo.mod is a resolvable module
		// (no git repo yet, no go.mod) — that is a real state an editor can
		// be open on, not a bug to report as a diagnostic.
		return nil
	}

	var diags []Diagnostic
	for _, c := range p.Checks {
		severity, ok := planSeverity(c.Status)
		if !ok || c.Pos == nil {
			continue
		}
		line := max(c.Pos.Line-1, 0)
		col := max(c.Pos.Col-1, 0)
		diags = append(diags, Diagnostic{
			Range:    Range{Start: Position{Line: line, Character: col}, End: Position{Line: line, Character: col + 1}},
			Severity: severity,
			Message:  c.Detail,
		})
	}
	return diags
}

// planSeverity is how a check that did not pass shows in an editor. A passing
// check is not a problem, and says so with ok false.
func planSeverity(status plan.Status) (severity int, ok bool) {
	switch status {
	case plan.Fail:
		return SeverityError, true
	case plan.Warn:
		return SeverityWarning, true
	default:
		return 0, false
	}
}
