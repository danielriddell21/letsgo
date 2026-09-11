// Command reprodigest builds the reproducibility fixture and prints the
// resulting digests as JSON.
//
// It exists so that CI can compare artifacts produced on different operating
// systems. The in-process tests prove a machine agrees with itself; this
// proves two machines agree with each other, which is the claim a third party
// relies on when they run letsgo verify on hardware we do not control.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/repro"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "reprodigest:", err)
		os.Exit(1)
	}
}

func run() error {
	fixture := flag.String("fixture", "internal/repro/testdata/fixture", "module to build")
	work := flag.String("work", "", "work directory (default: a temporary directory)")
	flag.Parse()

	workDir := *work
	if workDir == "" {
		dir, err := os.MkdirTemp("", "reprodigest-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		workDir = dir
	}

	// Fixed inputs. Every value that feeds the build is stated here rather
	// than read from the environment, so the only thing that varies between
	// two runs of this command is the machine it runs on.
	artifacts, err := repro.Build(context.Background(), repro.Options{
		ModuleDir:  *fixture,
		Package:    ".",
		Name:       "fixture",
		Version:    "1.2.3",
		Commit:     "9f2ab1c",
		ModTime:    time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC),
		ExtraFiles: []string{"README.md", "LICENSE"},
		WorkDir:    workDir,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(artifacts)
}
