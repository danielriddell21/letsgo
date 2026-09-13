package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// repository is where letsgo updates itself from. Hard-coded on purpose: an
// updater that takes the repository from a flag is an updater that can be
// pointed at somebody else's binary.
const repository = "danielriddell21/letsgo"

// updater is the decision an update run makes, separated from the flags,
// the terminal and the filesystem around it.
//
// A self-replacing binary is worth being able to test: "with --check nothing
// is installed" and "answering no installs nothing" are the two behaviours
// standing between a user and a surprise, and neither is checkable while the
// policy is tangled up with os.Stdout.
type updater struct {
	Options selfupdate.Options
	Current string

	// Check reports and installs nothing.
	Check bool

	// Confirm asks before installing. Nil installs without asking.
	Confirm func(version string) bool

	// Install applies the update. Injected so a test can assert whether it
	// was reached rather than replace its own binary to find out.
	Install func(ctx context.Context, u *selfupdate.Update) error
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	check := fs.Bool("check", false, "report whether a newer release exists, and change nothing")
	yes := fs.Bool("yes", false, "install without asking")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	tokenValue, _ := plan.Token(*token)

	u := updater{
		Options: selfupdate.Options{
			Repo:      repository,
			Current:   version,
			Token:     tokenValue,
			UserAgent: "letsgo/" + version,
		},
		Current: version,
		Check:   *check,
		Install: install,
	}
	if !*yes {
		u.Confirm = confirmUpdate
	}
	return u.run(context.Background(), os.Stdout)
}

func (u updater) run(ctx context.Context, w io.Writer) error {
	update, err := selfupdate.Check(ctx, u.Options)
	if err != nil {
		return err
	}
	if update == nil {
		fmt.Fprintf(w, "letsgo %s is the latest release\n", u.Current)
		return nil
	}

	describeUpdate(w, update, u.Current)

	if u.Check {
		return nil
	}
	if u.Confirm != nil && !u.Confirm(update.Version) {
		fmt.Fprintln(w, "\n  nothing was installed")
		return nil
	}

	fmt.Fprintln(w)
	if err := u.Install(ctx, update); err != nil {
		return err
	}
	fmt.Fprintf(w, "  letsgo %s installed\n", update.Version)
	return nil
}

// describeUpdate says what is on offer, and what will be checked before any of
// it is trusted.
func describeUpdate(w io.Writer, update *selfupdate.Update, current string) {
	fmt.Fprintf(w, "letsgo %s is available; this is %s\n", update.Version, current)
	fmt.Fprintf(w, "  %s\n", update.URL)
	fmt.Fprintf(w, "\n  %s\n", update.Archive)
	fmt.Fprintf(w, "    archive %s\n    binary  %s\n", short(update.SHA256), short(update.BinarySHA256))
	fmt.Fprintln(w, "\n  both digests come from the release's own manifest, and both are")
	fmt.Fprintln(w, "  checked before anything is installed.")
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func confirmUpdate(version string) bool {
	fmt.Printf("\n  install %s? [y/N] ", version)
	return readYes()
}

// install replaces the running binary, and says where it went: an updater that
// silently rewrites something on PATH should at least name it.
func install(ctx context.Context, update *selfupdate.Update) error {
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("letsgo: finding the running executable: %w", err)
	}
	if err := update.Apply(ctx); err != nil {
		return err
	}
	fmt.Printf("  %s\n", path)
	return nil
}
