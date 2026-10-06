package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/modsyntax"
)

// ConfigLine is one directive of a Plugin config, read with letsgo.mod's own
// grammar. A line inside a block is flattened: it carries the block's keyword
// and arguments ahead of its own, as if it had been written on one line, so a
// Plugin never walks blocks.
type ConfigLine struct {
	Keyword string
	Args    []string

	// Pos is where the line starts in the file, for an error that should
	// read path:line:col.
	Pos modsyntax.Position
}

// Config is a Plugin's own config, read and flattened but not interpreted: no
// keyword has a meaning here, and each Plugin keeps its own vocabulary and its
// own rule about repeats.
type Config struct {
	// Path is the file the lines came from, so an error can name it.
	Path  string
	Lines []ConfigLine
}

// Errorf formats an error at a line, as path:line:col: message.
func (c *Config) Errorf(l ConfigLine, format string, args ...any) error {
	return &modsyntax.SyntaxError{File: c.Path, Pos: l.Pos, Msg: fmt.Sprintf(format, args...)}
}

// LoadConfig reads a Plugin's own config: name.mod in configDir, falling back
// to the legacy letsgo-name.mod in the repository root, which is the parent of
// configDir (letsgo always sets it to <root>/.letsgo).
//
// An empty configDir is an error rather than a lookup in the working
// directory. When neither file exists the error satisfies
// errors.Is(err, fs.ErrNotExist), so a Plugin can decide for itself whether a
// missing config is fatal. A file that does not parse returns a
// *modsyntax.SyntaxError naming the path.
func LoadConfig(configDir, name string) (*Config, error) {
	if configDir == "" {
		return nil, errors.New("plugin config: no config directory given")
	}
	candidates := []string{
		filepath.Join(configDir, name+".mod"),
		filepath.Join(filepath.Dir(configDir), "letsgo-"+name+".mod"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return parseConfig(path, data)
	}
	last := candidates[len(candidates)-1]
	return nil, fmt.Errorf("%s: %w", last, fs.ErrNotExist)
}

func parseConfig(path string, data []byte) (*Config, error) {
	file, err := modsyntax.Parse(path, data)
	if err != nil {
		return nil, err
	}

	cfg := &Config{Path: path}
	for _, stmt := range file.Stmts {
		switch s := stmt.(type) {
		case *modsyntax.Line:
			cfg.Lines = append(cfg.Lines, ConfigLine{Keyword: s.Keyword, Args: s.Args, Pos: s.P})
		case *modsyntax.Block:
			for _, l := range s.Lines {
				args := append(append([]string(nil), s.Args...), l.Args...)
				cfg.Lines = append(cfg.Lines, ConfigLine{Keyword: s.Keyword, Args: args, Pos: l.P})
			}
		}
	}
	return cfg, nil
}
