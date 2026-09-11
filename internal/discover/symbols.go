package discover

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SymbolStatus describes whether a package-level identifier can carry a value
// injected by the linker's -X flag.
type SymbolStatus string

const (
	// SymbolOK means -X will work.
	SymbolOK SymbolStatus = "ok"

	// SymbolMissing means no such package-level identifier exists. This is the
	// single most common silent failure in Go release tooling: the linker
	// accepts -X for a symbol that does not exist without complaint, and the
	// binary reports whatever default was compiled in.
	SymbolMissing SymbolStatus = "missing"

	// SymbolNotString means the identifier exists but is not a string. -X only
	// patches strings, and is silently ignored otherwise.
	SymbolNotString SymbolStatus = "not-a-string"

	// SymbolIsConst means the identifier is a constant. Constants are inlined
	// at compile time and there is no variable left for the linker to patch.
	SymbolIsConst SymbolStatus = "constant"

	// SymbolDynamicInit means the variable is a string, but its initialiser is
	// not a literal. The linker writes the injected value into the binary's
	// static data and package initialisation then overwrites it at run time,
	// so the value is set and immediately lost.
	SymbolDynamicInit SymbolStatus = "dynamic-initialiser"
)

// Symbol is the result of inspecting one -X target.
type Symbol struct {
	Name       string
	Status     SymbolStatus
	Detail     string
	Suggestion string
}

// OK reports whether the symbol can carry an injected value.
func (s Symbol) OK() bool { return s.Status == SymbolOK }

// InspectVars checks whether each named identifier in the package at dir can
// receive a linker-injected string.
func InspectVars(dir string, names []string) ([]Symbol, error) {
	fset := token.NewFileSet()

	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		// Test files cannot contribute symbols to the linked binary.
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("discover: parsing %s: %w", dir, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("discover: no Go package in %s", dir)
	}

	vars, consts, all := collectDecls(pkgs)

	out := make([]Symbol, 0, len(names))
	for _, name := range names {
		out = append(out, classify(name, vars, consts, all))
	}
	return out, nil
}

// initKind describes how a package-level variable is initialised, which is
// what decides whether the linker's value survives to run time.
type initKind int

const (
	initNone    initKind = iota // var x string
	initString                  // var x = "literal"
	initOther                   // var x = 5
	initDynamic                 // var x = f()
)

type varInfo struct {
	declaredType string // "" when the type is inferred from the initialiser
	init         initKind
}

func collectDecls(pkgs map[string]*ast.Package) (vars map[string]varInfo, consts map[string]bool, all []string) {
	vars = map[string]varInfo{}
	consts = map[string]bool{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, ident := range value.Names {
						all = append(all, ident.Name)

						if gen.Tok == token.CONST {
							consts[ident.Name] = true
							continue
						}
						if gen.Tok != token.VAR {
							continue
						}

						info := varInfo{init: initNone}
						if id, ok := value.Type.(*ast.Ident); ok {
							info.declaredType = id.Name
						}
						if i < len(value.Values) {
							info.init = classifyInit(value.Values[i])
						}
						vars[ident.Name] = info
					}
				}
			}
		}
	}

	sort.Strings(all)
	return vars, consts, all
}

func classifyInit(expr ast.Expr) initKind {
	lit, ok := expr.(*ast.BasicLit)
	if !ok {
		return initDynamic
	}
	if lit.Kind == token.STRING {
		return initString
	}
	return initOther
}

func classify(name string, vars map[string]varInfo, consts map[string]bool, all []string) Symbol {
	if consts[name] {
		return Symbol{
			Name:   name,
			Status: SymbolIsConst,
			Detail: "declared as a constant; constants are inlined at compile time, leaving nothing for the linker to patch",
		}
	}

	info, found := vars[name]
	if !found {
		s := Symbol{
			Name:   name,
			Status: SymbolMissing,
			Detail: "no package-level variable with this name",
		}
		if near := nearest(name, all); near != "" {
			s.Suggestion = "did you mean " + near + "?"
		}
		return s
	}

	// Checked before the type, and deliberately so. A variable initialised by
	// an expression is assigned during package initialisation, which runs
	// after the linker's value is already in the binary's static data. The
	// injected value is therefore overwritten whatever its type, so this is
	// the more specific and more useful diagnosis — and it is the only case
	// where the type cannot be determined from the syntax alone anyway.
	if info.init == initDynamic {
		return Symbol{
			Name:   name,
			Status: SymbolDynamicInit,
			Detail: "initialised by an expression rather than a literal; package initialisation overwrites the linker's value at run time",
		}
	}

	switch {
	case info.declaredType != "" && info.declaredType != "string":
		return Symbol{
			Name:   name,
			Status: SymbolNotString,
			Detail: "declared as " + info.declaredType + ", not string",
		}
	case info.declaredType == "" && info.init == initOther:
		return Symbol{
			Name:   name,
			Status: SymbolNotString,
			Detail: "initialised with a non-string literal",
		}
	case info.declaredType == "" && info.init == initNone:
		return Symbol{
			Name:   name,
			Status: SymbolNotString,
			Detail: "has neither a declared type nor an initialiser",
		}
	}

	return Symbol{Name: name, Status: SymbolOK}
}

// nearest finds the most plausible intended identifier.
//
// A case difference is the overwhelmingly common form of this mistake
// (main.Version against main.version), so it is checked first and exactly.
// Beyond that a small edit distance catches ordinary typos, which is worth
// having because the alternative diagnosis — "no such variable" — gives the
// reader nothing to act on.
func nearest(name string, all []string) string {
	for _, candidate := range all {
		if candidate != name && strings.EqualFold(candidate, name) {
			return candidate
		}
	}

	budget := 1
	if len(name) >= 5 {
		budget = 2
	}

	best, bestDistance := "", budget+1
	for _, candidate := range all {
		if candidate == name {
			continue
		}
		if d := editDistance(strings.ToLower(name), strings.ToLower(candidate)); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	if bestDistance <= budget {
		return best
	}
	return ""
}

// editDistance is Levenshtein distance over two short identifiers.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// MainPackage describes a command that will be built and shipped.
type MainPackage struct {
	// Dir is the absolute directory containing the package.
	Dir string

	// RelPath is the package pattern relative to the module root, e.g.
	// "./cmd/letsgo".
	RelPath string

	// BinaryName is the name the compiled binary takes.
	BinaryName string
}

// FindMainPackages locates the commands to build.
//
// Convention first: ./cmd/* is where Go projects put their commands, and when
// it exists it is authoritative. Otherwise the module root is used when it is
// itself a main package. Anything more elaborate is what a config file is for.
func FindMainPackages(moduleDir string, moduleName string) ([]MainPackage, error) {
	cmdDir := filepath.Join(moduleDir, "cmd")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		var found []MainPackage
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(cmdDir, entry.Name())
			if isMainPackage(dir) {
				found = append(found, MainPackage{
					Dir:        dir,
					RelPath:    "./cmd/" + entry.Name(),
					BinaryName: entry.Name(),
				})
			}
		}
		if len(found) > 0 {
			sort.Slice(found, func(i, j int) bool { return found[i].RelPath < found[j].RelPath })
			return found, nil
		}
	}

	if isMainPackage(moduleDir) {
		return []MainPackage{{Dir: moduleDir, RelPath: ".", BinaryName: moduleName}}, nil
	}

	return nil, fmt.Errorf("discover: no main package found in %s or %s/cmd/*", moduleDir, moduleDir)
}

func isMainPackage(dir string) bool {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.PackageClauseOnly)
	if err != nil {
		return false
	}
	_, ok := pkgs["main"]
	return ok
}
