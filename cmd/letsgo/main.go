// Command letsgo builds and publishes Go releases.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// version is replaced at link time. It is declared exactly the way letsgo
// expects its users to declare it, so the tool releases itself the same way it
// releases anything else.
var version = "dev"

const usage = `letsgo builds and publishes Go releases.

usage:
  letsgo plan [--explain] [--snapshot]   resolve and check a release without performing one
  letsgo build [--snapshot] [-o dir]     build, archive and checksum into dist/
  letsgo fmt [file]                      format letsgo.mod
  letsgo version                         print the version

run a command with -h for its options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	command, args := os.Args[1], os.Args[2:]

	var err error
	switch command {
	case "plan":
		err = runPlan(args)
	case "build":
		err = runBuild(args)
	case "fmt":
		err = runFmt(args)
	case "version":
		fmt.Println("letsgo", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "letsgo: unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "letsgo:", err)
		os.Exit(1)
	}
}

// errPlanFailed marks a failure already reported in full by the plan output,
// so main does not print a second, vaguer version of the same thing.
var errPlanFailed = errors.New("plan failed")

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	explain := fs.Bool("explain", false, "show where each resolved value came from")
	snapshot := fs.Bool("snapshot", false, "plan an untagged working version")
	allowDirty := fs.Bool("allow-dirty", false, "permit an unclean worktree")
	if err := fs.Parse(args); err != nil {
		return err
	}

	started := time.Now()
	p, err := plan.Resolve(context.Background(), plan.Options{
		Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty,
	})
	if err != nil {
		return err
	}

	p.Report(os.Stdout, *explain)

	elapsed := took(started)
	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", elapsed)
		return errPlanFailed
	}
	fmt.Printf("\n  plan ok in %s · run `letsgo build` to produce artifacts\n", elapsed)
	return nil
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	snapshot := fs.Bool("snapshot", false, "build an untagged working version")
	allowDirty := fs.Bool("allow-dirty", false, "permit an unclean worktree")
	out := fs.String("o", "dist", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	started := time.Now()

	p, err := plan.Resolve(ctx, plan.Options{Dir: ".", Snapshot: *snapshot, AllowDirty: *allowDirty})
	if err != nil {
		return err
	}
	p.Report(os.Stdout, false)

	if !p.OK() {
		fmt.Printf("\n  plan failed in %s · nothing was built\n", took(started))
		return errPlanFailed
	}

	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}

	var artifacts []build.Artifact
	for _, cmd := range p.Commands {
		name := p.Project
		if len(p.Commands) > 1 {
			name = cmd.BinaryName
		}

		produced, err := build.Run(ctx, build.Options{
			ModuleDir: p.Module.Dir,
			Package:   cmd.RelPath,
			Name:      name,
			Version:   p.Version,
			Commit:    p.Git.ShortCommit,

			// The commit timestamp, never the clock. This is what makes the
			// same commit produce the same bytes tomorrow.
			ModTime: p.Git.CommitTime,

			Targets:    p.Targets,
			ExtraFiles: p.Files,
			WorkDir:    dir,
		})
		if err != nil {
			return err
		}
		artifacts = append(artifacts, produced...)
	}

	if _, err := build.WriteChecksums(dir, artifacts); err != nil {
		return err
	}

	fmt.Printf("\n  built %d artifacts in %s\n", len(artifacts)+1, took(started))
	for _, a := range artifacts {
		fmt.Printf("    %s  %s\n", a.ArchiveSHA256[:12], a.Archive)
	}
	fmt.Printf("    %s\n", build.ChecksumFile)
	fmt.Printf("\n  %s\n", dir)
	return nil
}

// took formats an elapsed duration at a resolution a person cares about.
// Rounding everything to tenths of a second reports a plan that finished in
// forty milliseconds as "0s", which reads like the tool did nothing.
func took(started time.Time) time.Duration {
	elapsed := time.Since(started)
	if elapsed < time.Second {
		return elapsed.Round(time.Millisecond)
	}
	return elapsed.Round(100 * time.Millisecond)
}

func runFmt(args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := plan.ConfigFile
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, err := config.Parse(filepath.Base(path), data)
	if err != nil {
		return err
	}
	// Decoding is not needed to format, but formatting a file that cannot be
	// decoded would tidy something meaningless into something meaningless and
	// well-indented.
	if _, err := config.Decode(file); err != nil {
		return err
	}

	formatted := file.Format()
	if string(formatted) == string(data) {
		return nil
	}
	return os.WriteFile(path, formatted, 0o644)
}
