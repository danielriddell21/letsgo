package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
)

// parseFlags parses a command's flags.
//
// A wrapper rather than a bare call at each site: every subcommand does this,
// and an error out of the flag package reaching the user unprefixed would not
// say which tool produced it.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(permute(fs, args)); err != nil {
		return fmt.Errorf("letsgo %s: %w", fs.Name(), err)
	}
	return nil
}

// permute moves flags ahead of positional arguments.
//
// Go's flag package stops at the first non-flag argument, so `letsgo yank
// v1.2.3 --reason "..."` would treat the flag as another operand. Every one of
// these commands documents its operand first, and typing it that way should
// not silently mean something else.
func permute(fs *flag.FlagSet, args []string) []string {
	var flags, operands []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		// Everything after "--" is an operand by definition, and stays in
		// order behind the terminator.
		case arg == "--":
			operands = append(operands, args[i+1:]...)
			return append(flags, append([]string{"--"}, operands...)...)

		case len(arg) > 1 && arg[0] == '-':
			flags = append(flags, arg)

			name := strings.TrimLeft(arg, "-")
			if strings.Contains(name, "=") {
				continue // the value is attached
			}
			// A non-boolean flag takes the next argument with it, or the
			// reordering would separate a flag from its value.
			if f := fs.Lookup(name); f != nil && !boolFlag(f) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}

		default:
			operands = append(operands, arg)
		}
	}
	return append(flags, operands...)
}

func boolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// hideFromUsage keeps a flag working but out of the command's --help.
func hideFromUsage(fs *flag.FlagSet, name string) {
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		fs.VisitAll(func(f *flag.Flag) {
			if f.Name == name {
				return
			}
			fmt.Fprintf(fs.Output(), "  -%s\n    \t%s\n", f.Name, f.Usage)
		})
	}
}

// errUsage reports a command invoked with the wrong arguments.
func errUsage(usage string) error {
	return fmt.Errorf("usage: %s", usage)
}

// machineConfig is the global config, or an empty one when it cannot be
// read: a broken global file is plan's to report, not every command's.
func machineConfig() *config.Global {
	global, err := config.LoadGlobal()
	if err != nil {
		return &config.Global{}
	}
	return global
}
