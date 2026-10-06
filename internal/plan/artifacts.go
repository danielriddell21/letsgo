package plan

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/install"
)

// archiveFiles is the check that reports what the archives will carry beside
// the binaries, however that list was arrived at.
const archiveFiles = "archive files"

func (p *Plan) resolveFiles(ctx context.Context) {
	if len(p.Config.ArchiveFiles) > 0 {
		files, err := p.expandArchiveFiles(ctx, p.Config.ArchiveFiles)
		if err != nil {
			p.addAt(p.posOf("archive"), archiveFiles, Fail, "%v", err)
			return
		}
		p.Files = files
		p.note(archiveFiles, strings.Join(p.Files, ", "), config.FileName)
		return
	}

	// Conventional documentation, included when present. The list lives in
	// internal/build so verification reaches the same answer from the same
	// tree rather than keeping a second copy of it.
	p.Files = build.FindDocumentation(p.RootDir)

	if len(p.Files) > 0 {
		p.note(archiveFiles, strings.Join(p.Files, ", "), "found in the repository root")
	}
}

// expandArchiveFiles turns the configured entries into the exact file list the
// archives will contain.
//
// A directory expands to the tracked files beneath it, sorted. That is pinned
// by the commit as tightly as a literal list is — the tree at a commit is
// fixed — while staying correct as the directory changes, which a hand-written
// list does not: adding a file to it and forgetting the config ships a release
// missing the file, and nothing fails, because nothing was asked for.
//
// A glob is refused rather than expanded. It would resolve against whatever is
// on disk at release time, which is an input the config does not pin.
func (p *Plan) expandArchiveFiles(ctx context.Context, entries []string) ([]string, error) {
	var tracked []string

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.ContainsAny(entry, "*?[") {
			return nil, fmt.Errorf(
				"archive %s: archive takes paths, not patterns; name a file or a directory", entry)
		}

		info, err := os.Stat(filepath.Join(p.RootDir, filepath.FromSlash(entry)))
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", entry, err)
		}
		if !info.IsDir() {
			out = append(out, entry)
			continue
		}

		// Read once, and only when a directory is actually named.
		if tracked == nil {
			if tracked, err = git.New(p.GitBin, p.RootDir).TrackedFiles(ctx); err != nil {
				return nil, fmt.Errorf("archive %s: %w", entry, err)
			}
		}

		under := filesUnder(tracked, entry)
		if len(under) == 0 {
			return nil, fmt.Errorf("archive %s: the directory holds no tracked files", entry)
		}
		out = append(out, under...)
	}

	sort.Strings(out)
	return out, nil
}

// filesUnder returns the tracked files inside dir, which git already reports
// in sorted, slash-separated form.
func filesUnder(tracked []string, dir string) []string {
	prefix := path.Clean(filepath.ToSlash(dir)) + "/"

	var found []string
	for _, name := range tracked {
		if strings.HasPrefix(name, prefix) {
			found = append(found, name)
		}
	}
	return found
}

// checkNotesRequired makes a required notes section's absence a Fail. Both
// sections follow the changelog, so `disable changelog` leaves them unwritten;
// a release that required one asked for exactly that not to happen.
func (p *Plan) checkNotesRequired() {
	if p.Features.On(feature.Changelog) {
		return
	}
	for _, name := range []feature.Name{feature.DiffNotes, feature.Randomart} {
		if p.required(name) && p.Features.On(name) {
			p.addAt(p.posOf("require "+string(name)), string(name), Fail, "required, but changelog is disabled, so no release notes are written")
		}
	}
}

// installScript is the check name checkInstallScriptRequired reports under.
const (
	installScript = "install script"

	// The names a check and its directive go by in the config.
	pluginDirective      = "plugin "
	requireInstallScript = "require install-script"
	brewTap              = "brew tap"
)

// checkInstallScriptRequired makes install.sh's absence a Fail rather than
// silent, for a repository that required it.
//
// Every other case (no tag, no target platform install.sh supports) is
// already how writeInstaller decides not to write one; require only asks
// that letsgo say so at plan time instead of after a release that shipped
// without it.
func (p *Plan) checkInstallScriptRequired() {
	if !p.required(feature.InstallScript) {
		return
	}

	switch {
	case p.Tag == "":
		p.addAt(p.posOf(requireInstallScript), installScript, Fail, "required, but there is no tag to build one for")
	case !p.HasRepo || p.Repo.Host != "github.com":
		p.addAt(p.posOf(requireInstallScript), installScript, Fail, "required, but the repository is not on GitHub")
	case len(installablePlatforms(p.Targets)) == 0:
		p.addAt(p.posOf(requireInstallScript), installScript, Fail, "required, but no built target is one install.sh supports")
	default:
		p.add(installScript, Pass, "will be generated")
	}
}

// installablePlatforms is which of the release's targets install.sh would
// carry, reusing the installer's own filter so the two can never disagree.
func installablePlatforms(targets []gobuild.Target) []string {
	ts := make([]install.Target, len(targets))
	for i, t := range targets {
		ts[i] = install.Target{OS: t.OS, Arch: t.Arch}
	}
	return install.Platforms(ts)
}

func (p *Plan) resolveArtifacts(ctx context.Context) {
	if p.Version == "" || len(p.Commands) == 0 {
		return
	}

	p.Groups = p.defaultGroups()
	p.applyLayoutPlugin(ctx)
	p.addVariantGroups(ctx)

	for _, group := range p.Groups {
		for _, target := range group.Targets {
			format := archive.FormatTarGz
			if target.OS == "windows" {
				format = archive.FormatZip
			}
			p.Artifacts = append(p.Artifacts, Artifact{
				Name: fmt.Sprintf("%s_%s_%s_%s%s",
					group.Name, p.Version, target.OS, target.Arch, format.Ext()),
				Target:   target,
				Commands: group.Commands,
				Format:   format,
			})
		}
	}
}

// defaultGroups is one archive per command, which is what letsgo has always
// produced and what a single-command repository wants.
func (p *Plan) defaultGroups() []Group {
	groups := make([]Group, 0, len(p.Commands))
	for _, cmd := range p.Commands {
		name := p.Project
		// With several commands the project name cannot identify an artifact.
		if len(p.Commands) > 1 {
			name = cmd.BinaryName
		}
		groups = append(groups, Group{
			Name:     name,
			Commands: []discover.MainPackage{cmd},
			Targets:  p.Targets,
			Tags:     p.Tags,
		})
	}
	return groups
}

// addVariantGroups appends a second build of the same commands for each
// variant.
//
// The commands and the layout are the release's; only how they compile and
// where they run differ. A repository with a headless CLI and a GUI build
// behind a tag ships both from one commit, and the suffix is what keeps their
// archives apart.
func (p *Plan) addVariantGroups(ctx context.Context) {
	if len(p.Config.Variants) == 0 {
		return
	}

	base := len(p.Groups)
	names := make([]string, 0, len(p.Config.Variants))

	for _, variant := range p.Config.Variants {
		targets, err := gobuild.ParseTargets(variant.Targets)
		if err != nil {
			p.variantFailed(variant.Name, err)
			return
		}
		if err := gobuild.Validate(ctx, "", targets); err != nil {
			p.variantFailed(variant.Name, err)
			return
		}

		for _, group := range p.Groups[:base] {
			p.Groups = append(p.Groups, Group{
				Name:     group.Name + "-" + variant.Name,
				Commands: group.Commands,
				Targets:  targets,
				// The release's tags first: a variant adds to how the module
				// builds rather than replacing it.
				Tags:    append(append([]string{}, p.Tags...), variant.Tags...),
				Variant: variant.Name,
			})
		}
		names = append(names, variant.Name)
	}

	p.note("variants", strings.Join(names, ", "), config.FileName)
	p.add("variants", Pass, "%d variant(s): %s", len(names), strings.Join(names, ", "))
}

// variantFailed reports a problem with one variant, naming which. A repository
// can declare several, and a message that did not say which one would leave
// the reader to guess.
func (p *Plan) variantFailed(name string, err error) {
	p.add("variants", Fail, "variant %s: %v", name, err)
}

func summarise(targets []gobuild.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.String()
	}
	return strings.Join(names, ", ")
}

// sumdbCheck is the check name checkSumdb reports under, and feature.Sumdb the
// feature behind it.
const (
	sumdbCheck = "sumdb"
)

// checkSumdb records why the sum.golang.org cross-check will not run, so a
// required sumdb turns that into a Fail. Nothing is added when it will run:
// the check itself happens during release, after the module proxy has been
// primed and before any asset is attached.
func (p *Plan) checkSumdb() {
	d := gate.DecideSumdb(gate.SumdbInput{
		Disabled:     !p.Features.On(feature.Sumdb),
		Snapshot:     p.Snapshot,
		Untagged:     p.Tag == "",
		Draft:        p.Config.Draft,
		Scoped:       p.Config.ModuleDir != "",
		ProxyWarmOff: !p.Features.On(feature.ProxyWarm),
		ModulePath:   p.Module.Path,
	})
	switch {
	case d.Run:
	case d.ByConfig:
		p.add(sumdbCheck, Skip, disabledByConfig)
	default:
		p.skip(sumdbCheck, feature.Sumdb, "%s", d.Reason)
	}
}
