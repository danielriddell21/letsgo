package main

import (
	"encoding/json"
	"flag"
	"fmt"
)

// capabilities are what this build offers an editor or CI, so a client asks
// the binary instead of keeping its own table of which release added what.
var capabilities = []string{"plan-json", "tag-json", "tag-ref", "plugin-install", "lsp", "update-pin", "did-you-mean"}

func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "print the version and capabilities as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if !*jsonOutput {
		fmt.Println("letsgo", version)
		return nil
	}
	data, err := json.MarshalIndent(struct {
		Schema       int      `json:"schema"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}{1, version, capabilities}, "", "  ")
	if err != nil {
		return fmt.Errorf("letsgo version: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
