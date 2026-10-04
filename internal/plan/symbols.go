package plan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
)

// resolveLDFlags injects version metadata only into variables that can
// actually receive it.
//
// The linker accepts -X for a symbol that does not exist without complaint, so
// a wrong name produces a binary reporting its compiled-in default forever.
// Checking first turns that silent failure into a message at plan time, and
// injecting only what works means a project that does not declare these
// variables is not handed flags that do nothing.
// VersionSymbols names the variables the version metadata is injected into,
// fully qualified as the linker writes them.
type VersionSymbols struct {
	Version string
	Commit  string
	Date    string
}

func (p *Plan) resolveLDFlags(ctx context.Context) {
	p.LDFlags = append(p.LDFlags, p.Config.LDFlags...)
	p.applyLDFlagsPlugin(ctx)
	p.Symbols = VersionSymbols{Version: "main.version", Commit: "main.commit", Date: "main.date"}

	if p.Config.Version != nil {
		p.resolveVersionSymbols()
		return
	}
	p.checkMainVersionVars()
}

// versionInjection is the check that reports which variables the release will
// write its version into, whether the config named them or letsgo inferred
// them from main.
const versionInjection = "version injection"

// resolveVersionSymbols checks the variables the config named.
//
// A configured symbol is checked harder than an inferred one: asking for
// injection into a variable that does not exist is a mistake, where simply not
// declaring main.version is a choice. Getting this wrong is exactly the silent
// failure that ships binaries reporting their compiled-in default.
func (p *Plan) resolveVersionSymbols() {
	targets := []struct {
		label  string
		symbol string
		into   *string
	}{
		{"version", p.Config.Version.Version, &p.Symbols.Version},
		{"commit", p.Config.Version.Commit, &p.Symbols.Commit},
		{"date", p.Config.Version.Date, &p.Symbols.Date},
	}

	var problems []string
	checked := 0

	for _, t := range targets {
		if t.symbol == "" {
			continue
		}
		qualified, dir, err := p.locateSymbol(t.symbol)
		if err != nil {
			problems = append(problems, fmt.Sprintf("version %s: %v", t.label, err))
			continue
		}

		_, name, _ := cutSymbol(qualified)
		symbols, err := discover.InspectVars(dir, []string{name})
		if err != nil {
			problems = append(problems, fmt.Sprintf("version %s: %v", t.label, err))
			continue
		}

		sym := symbols[0]
		if sym.Status != discover.SymbolOK {
			detail := fmt.Sprintf("version %s: %s: %s", t.label, qualified, sym.Detail)
			if sym.Status == discover.SymbolMissing {
				detail = fmt.Sprintf("version %s: %s is not declared", t.label, qualified)
			}
			if sym.Suggestion != "" {
				detail += " (" + sym.Suggestion + ")"
			}
			problems = append(problems, detail)
			continue
		}

		*t.into = qualified
		checked++
	}

	if len(problems) > 0 {
		p.addAt(p.posOf("version"), versionInjection, Fail, "%s", strings.Join(problems, "\n"))
		return
	}
	p.add(versionInjection, Pass, "%d symbol(s) verified before injection", checked)
	p.note("version symbols", strings.Join(p.injectedSymbols(), ", "), ConfigFile)
}

func (p *Plan) injectedSymbols() []string {
	return []string{p.Symbols.Version, p.Symbols.Commit, p.Symbols.Date}
}

// locateSymbol turns a configured symbol into the form the linker needs and
// the directory holding its package.
//
// A package path may be written whole or relative to the module, because a
// module path is long and repeating it in every entry is noise. Either way the
// package has to exist in this module: injecting into a variable letsgo cannot
// see would be the unchecked -X that the gate exists to prevent.
func (p *Plan) locateSymbol(symbol string) (qualified, dir string, err error) {
	pkg, name, ok := cutSymbol(symbol)
	if !ok || name == "" {
		return "", "", fmt.Errorf("%s must name a package and a variable, as in internal/buildinfo.Version", symbol)
	}

	rel := ""
	switch {
	case pkg == p.Module.Path:
	case strings.HasPrefix(pkg, p.Module.Path+"/"):
		rel = strings.TrimPrefix(pkg, p.Module.Path+"/")
	default:
		rel, pkg = pkg, p.Module.Path+"/"+pkg
	}

	dir = filepath.Join(p.Module.Dir, filepath.FromSlash(rel))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("%s is not a package in %s", pkg, p.Module.Path)
	}
	return pkg + "." + name, dir, nil
}

// cutSymbol splits a linker symbol at its final dot, which is where the linker
// splits it: a package path contains dots of its own.
func cutSymbol(symbol string) (pkg, name string, ok bool) {
	i := strings.LastIndex(symbol, ".")
	if i <= 0 {
		return "", "", false
	}
	return symbol[:i], symbol[i+1:], true
}

// checkMainVersionVars is the inferred path: main.version, main.commit and
// main.date in each command, injected where they are declared.
func (p *Plan) checkMainVersionVars() {
	var problems []string
	injected := 0

	for _, cmd := range p.Commands {
		names := make([]string, len(versionVars))
		copy(names, versionVars)

		symbols, err := discover.InspectVars(cmd.Dir, names)
		if err != nil {
			p.add(versionInjection, Warn, "%v", err)
			return
		}

		for _, sym := range symbols {
			switch sym.Status {
			case discover.SymbolOK:
				injected++
			case discover.SymbolMissing:
				// Not declaring these is a legitimate choice, so their absence
				// is silent. Declaring one wrongly is not.
			default:
				detail := fmt.Sprintf("main.%s in %s: %s", sym.Name, cmd.RelPath, sym.Detail)
				if sym.Suggestion != "" {
					detail += " (" + sym.Suggestion + ")"
				}
				problems = append(problems, detail)
			}
		}
	}

	switch {
	case len(problems) > 0:
		p.add(versionInjection, Fail, "%s", strings.Join(problems, "\n"))
	case injected == 0:
		p.add(versionInjection, Skip,
			"no main.version, main.commit or main.date declared; name one with `version <symbol>`")
	default:
		p.add(versionInjection, Pass, "%d symbol(s) verified before injection", injected)
	}
}
