package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
)

// publishTap writes a formula to the configured Homebrew tap, one per command
// the module builds.
//
// A module with several commands gets several formulas rather than a refusal:
// a formula installs one archive per platform, so `alpha` and `beta` cannot
// share one, and `Formula/alpha.rb` beside `Formula/beta.rb` is what a tap is
// shaped to hold anyway.
func publishTap(ctx context.Context, p *plan.Plan, result *release.Result, api brew.FileAPI, client *github.Client, repo github.Repo) error {
	if p.Tap == (github.Repo{}) {
		return nil
	}

	// Only a published release serves assets from the download URLs a formula
	// names. Pointing a tap at a draft would produce a formula that resolves
	// to a 404 for everyone but its author.
	if p.Config.Draft {
		fmt.Println("  ! skipped the Homebrew formula: a draft release serves no public assets")
		return nil
	}

	info := describeRepo(ctx, client, repo)

	for _, formula := range formulas(p, result, repo, info) {
		published, err := brew.Publish(ctx, api, p.Tap, formula)
		if err != nil {
			return err
		}
		fmt.Printf("  %s %s in %s\n", published.Status, published.Path, p.Tap)
	}
	return nil
}

// formulas builds one formula per archive, grouping the artifacts by the
// archive they belong to.
//
// Per archive rather than per binary, because a formula names one archive per
// platform: an archive holding eleven tools is one formula that installs
// eleven binaries, not eleven formulas fighting over the same file.
func formulas(p *plan.Plan, result *release.Result, repo github.Repo, info *github.RepoInfo) []brew.Formula {
	tag := releaseTag(p)

	order := make([]string, 0, len(p.Groups))
	platforms := map[string][]brew.Platform{}
	binaries := map[string][]string{}

	for _, a := range result.Artifacts {
		name := formulaName(a, p.Version)
		if _, seen := platforms[name]; !seen {
			order = append(order, name)
			for _, b := range a.Binaries {
				binaries[name] = append(binaries[name], b.Name)
			}
		}
		platforms[name] = append(platforms[name], brew.Platform{
			OS: a.OS, Arch: a.Arch,
			URL:    github.DownloadURL(repo, tag, a.Archive),
			SHA256: a.ArchiveSHA256,
		})
	}

	out := make([]brew.Formula, 0, len(order))
	for _, name := range order {
		formula := brew.Formula{
			Name:      name,
			Binaries:  binaries[name],
			Version:   p.Version,
			Homepage:  "https://" + p.Repo.String(),
			Platforms: platforms[name],
		}
		if info != nil {
			formula.Description, formula.License = info.Description, info.License
			if info.Homepage != "" {
				formula.Homepage = info.Homepage
			}
		}
		out = append(out, formula)
	}
	return out
}

// formulaName is the archive's base name, which is what the formula is called.
func formulaName(a build.Artifact, version string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(a.Archive, ".tar.gz"), ".zip")
	return strings.TrimSuffix(name, fmt.Sprintf("_%s_%s_%s", version, a.OS, a.Arch))
}

// describeRepo reads the description and licence the formula should carry.
//
// Failure is not fatal: `desc` and `license` are optional in a formula, and a
// release should not stop because a metadata endpoint did. Returning nil means
// the formula is rendered without them.
func describeRepo(ctx context.Context, client *github.Client, repo github.Repo) *github.RepoInfo {
	info, err := client.Repository(ctx, repo)
	if err != nil {
		fmt.Printf("  ! could not read %s's description for the formula: %v\n", repo, err)
		return nil
	}
	return info
}

// publishImages pushes the container images the build assembled.
//
// A rehearsal prints what would be pushed and pushes nothing. It can be
// specific rather than hand-waving because the digests are already fixed:
// assembly happened during the build, so the rehearsal names the exact image a
// real run would publish.
func publishImages(ctx context.Context, p *plan.Plan, result *release.Result, token string, snapshot bool) error {
	if len(result.Images) == 0 {
		return nil
	}

	if snapshot {
		fmt.Print("\n  images that would be pushed\n")
		fmt.Print(release.Describe(result.Images))
		return nil
	}
	if p.Config.Draft {
		fmt.Println("  ! skipped the container image: a draft release should not publish a public tag")
		return nil
	}

	return release.PushImages(ctx, result.Images, token, func(format string, args ...any) {
		fmt.Printf("  "+format+"\n", args...)
	})
}
