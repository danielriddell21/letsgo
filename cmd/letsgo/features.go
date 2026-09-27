package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/danielriddell21/letsgo/internal/feature"
)

func runFeatures(args []string) error {
	fs := flag.NewFlagSet("features", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return listFeatures(os.Stdout)
}

// listFeatures prints the catalogue: what each feature is called, its
// default state, and how a repository would change it.
//
// This is the catalogue only. Nothing here yet reads letsgo.mod, so every
// feature is shown at its default — resolving a repository's own
// disable/require directives is a later slice.
func listFeatures(w io.Writer) error {
	for _, f := range feature.All {
		fmt.Fprintf(w, "%-14s %-9s %-3s %s\n", f.Name, f.Kind, onOff(f.Default), changeHint(f))
	}
	return nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// changeHint says what a repository would write to move a feature off its
// default, so the catalogue is also the answer to "how do I change this".
func changeHint(f feature.Feature) string {
	if f.Enable != "" {
		return fmt.Sprintf("enable with `%s`", f.Enable)
	}

	var how []string
	if f.Disable {
		how = append(how, "disable")
	}
	if f.Require {
		how = append(how, "require")
	}
	if len(how) == 0 {
		return "cannot be changed"
	}
	return strings.Join(how, ", ")
}
