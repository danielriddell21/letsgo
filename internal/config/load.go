package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/modsyntax"
)

// FileName is the optional configuration file letsgo reads, beside go.mod.
const FileName = "letsgo.mod"

// ErrNotFound is returned by Load when moduleDir has no letsgo.mod. Absence is
// the primary path, not a failure: a caller usually falls back to zero-config
// defaults. Any other error from Load is a real one.
var ErrNotFound = errors.New(FileName + " not found")

// Load reads and decodes moduleDir's letsgo.mod, returning the config and the
// path it was read from (set even when the file is missing).
//
// Only a missing file returns ErrNotFound. Every other read error, a
// permission problem for instance, is returned as is, and a file that does
// not parse or decode returns that error, naming FileName.
func Load(moduleDir string) (*Config, string, error) {
	path := filepath.Join(moduleDir, FileName)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, path, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	if err != nil {
		return nil, path, err
	}

	file, err := modsyntax.Parse(FileName, data)
	if err != nil {
		return nil, path, err
	}
	cfg, err := Decode(file)
	if err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}
