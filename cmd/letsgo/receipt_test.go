package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestHideFromUsageOmitsOnlyTheNamedFlag(t *testing.T) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var out bytes.Buffer
	fs.SetOutput(&out)
	fs.Bool("json", false, "print the report as JSON")
	fs.Bool("receipt", false, "")
	hideFromUsage(fs, "receipt")

	fs.Usage()

	got := out.String()
	if !strings.Contains(got, "-json") {
		t.Errorf("usage lost a visible flag:\n%s", got)
	}
	if strings.Contains(got, "receipt") {
		t.Errorf("usage mentions the hidden flag:\n%s", got)
	}
	if err := fs.Parse([]string{"--receipt"}); err != nil {
		t.Errorf("hidden flag no longer parses: %v", err)
	}
}
