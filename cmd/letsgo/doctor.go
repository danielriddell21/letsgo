package main

import (
	"context"
	"flag"
	"os"

	"github.com/danielriddell21/letsgo/internal/doctor"
)

// runDoctor diagnoses the tools and repository state a release needs,
// read-only and offline: see docs/hld/doctor.md.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage("letsgo doctor")
	}

	result, err := doctor.Run(context.Background(), ".")
	if err != nil {
		return err
	}

	result.Report(os.Stdout)
	if !result.OK() {
		return errDoctorFailed
	}
	return nil
}
