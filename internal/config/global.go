package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// GlobalConfigEnvOverride names the global config file directly, overriding
// the default os.UserConfigDir()/letsgo/config.mod location.
const GlobalConfigEnvOverride = "LETSGO_CONFIG"

// Global is the semantic content of the global config.mod file: machine-level
// defaults that apply across every repository on this machine, chosen once by
// whoever administers it.
//
// Nothing here may reach a release's bytes, gates, version or publishing —
// that is what letsgo.mod is for, and DecodeGlobal rejects any directive that
// belongs there instead.
type Global struct {
	// Path is where this value was read from, or would be read from if the
	// file existed. Set by LoadGlobal, so callers can name the source of a
	// value without resolving the path a second time.
	Path string

	// Go and Git override the resolved toolchain and git binaries.
	Go  string
	Git string

	// Tools overrides where a named external gate tool is found, keyed by
	// tool name (e.g. "govulncheck").
	Tools map[string]string

	// CacheDir overrides the build cache location. CacheOff disables the
	// cache outright; when both are set, CacheOff wins.
	CacheDir string
	CacheOff bool

	// PluginsDir overrides the plugin store location.
	PluginsDir string

	// PluginRepo overrides the default repository `plugin install` installs
	// from, as "owner/repo".
	PluginRepo string

	// Proxy overrides the module proxy warmed after a release.
	Proxy string

	// TokenCommand, when set, is run to obtain a forge token once a flag and
	// the environment have both come up empty. Its output is never written
	// anywhere.
	TokenCommand []string

	// Color and UpdateCheck are presentation-only: how output is colored, and
	// how often letsgo checks for a newer release of itself.
	Color       string
	UpdateCheck string
}

// globalKnown lists every global directive, with its arity described for
// error messages. See known, in decode.go, for why this is kept separate
// from globalHandlers.
var globalKnown = map[string]string{
	"go":            "go <path>",
	"git":           "git <path>",
	"tool":          "tool <name> <path>",
	"cache":         "cache <dir>, or cache off",
	"plugins":       "plugins <dir>",
	"plugin-repo":   "plugin-repo <owner/repo>",
	"proxy":         "proxy <url>",
	"token-command": "token-command <argv...>",
	"color":         "color auto|always|never",
	"update-check":  "update-check off|daily|weekly",
}

// globalHandlers folds each global directive into a Global.
var globalHandlers = map[string]func(g *Global, file string, line *Line) error{
	"go":            applyGlobalGo,
	"git":           applyGlobalGit,
	"tool":          applyGlobalTool,
	"cache":         applyGlobalCache,
	"plugins":       applyGlobalPlugins,
	"plugin-repo":   applyGlobalPluginRepo,
	"proxy":         applyGlobalProxy,
	"token-command": applyGlobalTokenCommand,
	"color":         applyGlobalColor,
	"update-check":  applyGlobalUpdateCheck,
}

// globalDocs is a one-line explanation per global directive, for an editor's
// hover text. See docs, in decode.go, for why this is kept separate from
// globalHandlers.
var globalDocs = map[string]string{ //nolint:gosec // hover text, not a credential
	"go":            "Overrides the resolved go toolchain binary.",
	"git":           "Overrides the resolved git binary.",
	"tool":          "Overrides where a named gate tool is found.",
	"cache":         "Overrides the build cache location, or turns it off.",
	"plugins":       "Overrides the plugin store location.",
	"plugin-repo":   "Overrides the default repository `plugin install` installs from.",
	"proxy":         "Overrides the module proxy warmed after a release.",
	"token-command": "Runs to obtain a forge token once a flag and the environment both come up empty. Output is never written anywhere.",
	"color":         "How output is colored.",
	"update-check":  "Opt-in: how often letsgo checks for a newer release of itself and mentions it on stderr. Off by default; nothing is ever installed.",
}

// GlobalDoc returns a global directive's usage and documentation, for hover
// text. ok is false for an unknown keyword.
func GlobalDoc(keyword string) (usage, doc string, ok bool) {
	usage, ok = globalKnown[keyword]
	if !ok {
		return "", "", false
	}
	return usage, globalDocs[keyword], true
}

// GlobalDirectives lists every known global directive name, sorted.
func GlobalDirectives() []string {
	names := make([]string, 0, len(globalKnown))
	for name := range globalKnown {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DecodeGlobal interprets a parsed global config file.
//
// Its directive set is disjoint from letsgo.mod's: a directive that could
// change a release's bytes, gates, version or publishing is rejected by name
// rather than silently accepted, so a repository directive pasted into the
// wrong file fails loudly instead of quietly doing nothing.
func DecodeGlobal(f *File) (*Global, error) {
	g := &Global{}
	seen := map[string]Position{}

	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *Comment:
			continue
		case *Block:
			return nil, errAt(f.Name, s.Pos(), "%s takes no block in the global config", s.Keyword)
		case *Line:
			if err := decodeGlobalLine(g, f.Name, seen, s); err != nil {
				return nil, err
			}
		}
	}
	return g, nil
}

func decodeGlobalLine(g *Global, file string, seen map[string]Position, line *Line) error {
	if _, ok := known[line.Keyword]; ok {
		return errAt(file, line.P, "%s belongs in letsgo.mod, not the global config", line.Keyword)
	}
	if err := checkGlobalKnown(file, line.Keyword, line.P); err != nil {
		return err
	}
	// tool is repeatable, one name at a time; every other directive is a
	// single machine-wide setting, so repeating it is ambiguous.
	if line.Keyword != "tool" {
		if err := checkOnce(file, seen, line.Keyword, line.P); err != nil {
			return err
		}
	}
	return globalHandlers[line.Keyword](g, file, line)
}

func checkGlobalKnown(file, keyword string, pos Position) error {
	if _, ok := globalKnown[keyword]; ok {
		return nil
	}

	names := make([]string, 0, len(globalKnown))
	for name := range globalKnown {
		names = append(names, name)
	}
	sort.Strings(names)

	return unknownName(file, pos, "directive", keyword, names)
}

func applyGlobalGo(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	g.Go = line.Args[0]
	return nil
}

func applyGlobalGit(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	g.Git = line.Args[0]
	return nil
}

func applyGlobalTool(g *Global, file string, line *Line) error {
	if len(line.Args) != 2 {
		return globalArity(file, line)
	}
	name, path := line.Args[0], line.Args[1]
	if _, exists := g.Tools[name]; exists {
		return errAt(file, line.P, "tool %s is already set", name)
	}
	if g.Tools == nil {
		g.Tools = map[string]string{}
	}
	g.Tools[name] = path
	return nil
}

func applyGlobalCache(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	if line.Args[0] == "off" {
		g.CacheOff = true
		return nil
	}
	g.CacheDir = line.Args[0]
	return nil
}

func applyGlobalPlugins(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	g.PluginsDir = line.Args[0]
	return nil
}

func applyGlobalPluginRepo(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	if _, _, ok := strings.Cut(line.Args[0], "/"); !ok {
		return errAt(file, line.P, "plugin-repo %q must be in owner/repo form", line.Args[0])
	}
	g.PluginRepo = line.Args[0]
	return nil
}

func applyGlobalProxy(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	g.Proxy = line.Args[0]
	return nil
}

func applyGlobalTokenCommand(g *Global, file string, line *Line) error {
	if len(line.Args) == 0 {
		return globalArity(file, line)
	}
	g.TokenCommand = line.Args
	return nil
}

var globalColors = []string{"auto", "always", "never"}

func applyGlobalColor(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	if !slices.Contains(globalColors, line.Args[0]) {
		return errAt(file, line.P, "color %q must be one of %s", line.Args[0], strings.Join(globalColors, ", "))
	}
	g.Color = line.Args[0]
	return nil
}

var globalUpdateChecks = []string{"off", "daily", "weekly"}

func applyGlobalUpdateCheck(g *Global, file string, line *Line) error {
	if len(line.Args) != 1 {
		return globalArity(file, line)
	}
	if !slices.Contains(globalUpdateChecks, line.Args[0]) {
		return errAt(file, line.P, "update-check %q must be one of %s", line.Args[0], strings.Join(globalUpdateChecks, ", "))
	}
	g.UpdateCheck = line.Args[0]
	return nil
}

func globalArity(file string, line *Line) error {
	return errAt(file, line.P, "%s takes %s", line.Keyword, globalKnown[line.Keyword])
}

// GlobalPath is where LoadGlobal reads from: GlobalConfigEnvOverride if set,
// else os.UserConfigDir()/letsgo/config.mod. explicit reports whether the
// path came from the override, since a missing override is an error while a
// missing default location is not.
func GlobalPath() (path string, explicit bool, err error) {
	if override := os.Getenv(GlobalConfigEnvOverride); override != "" {
		return override, true, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", false, fmt.Errorf("config: %w", err)
	}
	return filepath.Join(dir, "letsgo", "config.mod"), false, nil
}

// LoadGlobal reads and decodes the global config file. Nothing is cached: the
// composition root reads it and hands the result to whatever needs it, so no
// state outlives the call and tests can vary the environment freely.
func LoadGlobal() (*Global, error) {
	path, explicit, err := GlobalPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if explicit {
				return nil, fmt.Errorf("config: %s=%q: %w", GlobalConfigEnvOverride, path, err)
			}
			return &Global{Path: path}, nil
		}
		return nil, fmt.Errorf("config: %w", err)
	}

	f, err := Parse(path, data)
	if err != nil {
		return nil, err
	}
	g, err := DecodeGlobal(f)
	if err != nil {
		return nil, err
	}
	g.Path = path
	return g, nil
}
