// Command letsgo builds and publishes Go releases.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/releaser"
)

// version is replaced at link time. It is declared exactly the way letsgo
// expects its users to declare it, so the tool releases itself the same way it
// releases anything else.
var version = "dev"

const usage = `letsgo builds and publishes Go releases.

usage:
  letsgo plan [--explain] [--json] [--publish] [--diff [--exit-code]] [--format md] [-out file]  resolve and check a release without performing one
  letsgo build [--snapshot] [-o dir]     build every artifact into dist/ without publishing
  letsgo release [--draft] [-o dir]      build and publish, resumably
  letsgo apply [file] [-auto-approve]   publish a release as a saved plan agreed it; with no file, plan it, show it and ask
  letsgo release --snapshot              rehearse a release without publishing
  letsgo verify [tag] [--json] [--words]  rebuild a published release and compare it
  letsgo doctor [--json]                 diagnose tools and repository state, read-only
  letsgo audit [<tag>]                   re-check published releases against today's vulnerability database
  letsgo diff <from> [to] [--format text|md|json]  compare two releases: size, dependencies, API
  letsgo promote <rc-tag>                rebuild a prerelease as a stable release
  letsgo yank <tag> [--reason "..."]     retract a release, including the go.mod directive
  letsgo plan -yank <tag> [-out file]    show what retracting a release would change
  letsgo tag [--major|--minor|--patch|--pre|--json]  work out the next version and tag it
  letsgo update [--check]                update letsgo itself, verified against its manifest
  letsgo plugin install [<name>]         install a plugin, verified against its manifest (or every pin, with none)
  letsgo plugin list [--json]            the plugins this repository pins, and what is installed
  letsgo plugin prune                    remove store entries no pin in this repository references
  letsgo features [--json]               the feature catalogue: what can be disabled or required
  letsgo fmt [file|-]                    format letsgo.mod; - reads stdin, writes to stdout
  letsgo lsp [--restricted]              serve letsgo.mod over stdio JSON-RPC, for an editor
  letsgo version                         print the version (also --version)

run a command with -h for its options.
`

// commands is the whole surface of the tool: one verb to the function that
// runs it, with the forge the commands that need one are wired to.
//
// A table rather than a switch, so that adding a verb is an entry here and not
// a change to the program's entry point. It is also why the aliases sit beside
// the names they alias instead of sharing a case.
func commands(f forge) map[string]func([]string) error {
	return map[string]func([]string) error{
		"plan":     f.runPlan,
		"build":    f.runBuild,
		"release":  f.runRelease,
		"apply":    f.runApply,
		"verify":   f.runVerify,
		"doctor":   f.runDoctor,
		"audit":    f.runAudit,
		"diff":     f.runDiff,
		"promote":  f.runPromote,
		"yank":     f.runYank,
		"update":   f.runUpdate,
		"plugin":   f.runPlugin,
		"tag":      f.runTag,
		"fmt":      runFmt,
		"features": runFeatures,
		"lsp":      f.runLSP,

		"version":   runVersion,
		"--version": runVersion,
		"-version":  runVersion,
		"-v":        runVersion,

		"help":   runHelp,
		"-h":     runHelp,
		"--help": runHelp,
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	command, args := os.Args[1], os.Args[2:]

	global, globalErr := loadGlobal()
	f := forge{global: global, globalErr: globalErr}
	run, ok := commands(f)[command]
	if !ok {
		fmt.Fprintf(os.Stderr, "letsgo: unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}

	code := 0
	if err := run(args); err != nil {
		code = exitCode(err)
		if code == 1 {
			fmt.Fprintln(os.Stderr, "letsgo:", err)
		}
	}
	if globalErr == nil {
		maybeNoticeUpdate(global, command, args)
	}
	os.Exit(code)
}

// exitCode is the status a failed command exits with. A plan that has changes
// is not a failure, and is told apart from one so that a script can act on
// drift without treating it as an error.
func exitCode(err error) int {
	if errors.Is(err, errPlanChanges) {
		return 2
	}
	return 1
}

func runHelp([]string) error {
	fmt.Print(usage)
	return nil
}

// errDoctorFailed likewise: the report already names every failing check.
var errDoctorFailed = errors.New("doctor found a problem")

// took formats an elapsed duration at a resolution a person cares about.
// Rounding everything to tenths of a second reports a plan that finished in
// forty milliseconds as "0s", which reads like the tool did nothing.
func took(started time.Time) time.Duration { return releaser.Took(started) }
