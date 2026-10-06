package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/modsyntax"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/plan"
)

func runFmt(args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	path := plan.ConfigFile
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	if path == "-" {
		return fmtStdin()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("letsgo: reading %s: %w", path, err)
	}
	formatted, err := formatConfig(filepath.Base(path), data)
	if err != nil {
		return err
	}
	if string(formatted) == string(data) {
		return nil
	}
	if err := os.WriteFile(path, formatted, 0o600); err != nil {
		return fmt.Errorf("letsgo: writing %s: %w", path, err)
	}
	return nil
}

// fmtStdin reads letsgo.mod from stdin and writes the formatted result to
// stdout, for format-on-save without writing the file behind the editor's
// back.
func fmtStdin() error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("letsgo: reading stdin: %w", err)
	}
	formatted, err := formatConfig(plan.ConfigFile, data)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(formatted); err != nil {
		return fmt.Errorf("letsgo: writing stdout: %w", err)
	}
	return nil
}

// formatConfig parses and decodes data before formatting it, so formatting a
// file that cannot be decoded doesn't tidy something meaningless into
// something meaningless and well-indented.
func formatConfig(name string, data []byte) ([]byte, error) {
	file, err := modsyntax.Parse(name, data)
	if err != nil {
		return nil, err
	}
	if _, err := config.Decode(file); err != nil {
		return nil, err
	}
	return file.Format(), nil
}
