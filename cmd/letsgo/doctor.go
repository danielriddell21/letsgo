package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/danielriddell21/letsgo/internal/doctor"
)

// runDoctor diagnoses the tools and repository state a release needs,
// read-only and offline: see docs/hld/doctor.md.
func (f forge) runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "print the report as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage("letsgo doctor")
	}

	result, err := doctor.Run(context.Background(), ".", f.machine())
	if err != nil {
		return err
	}

	if *jsonOutput {
		data, err := result.JSON()
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else {
		result.Report(os.Stdout)
	}

	if !result.OK() {
		return errDoctorFailed
	}
	return nil
}
