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

// tapTokenUsage documents --tap-token once, for the three commands that reach
// a tap.
const tapTokenUsage = "token the Homebrew tap is written with (default: $LETSGO_TAP_TOKEN, else the release token)"

// releaseTokenUsage documents --release-token once.
const releaseTokenUsage = "token the GitHub release is published with (default: $LETSGO_RELEASE_TOKEN, else --token)" //nolint:gosec // usage text, not a credential

// splitClientFor returns a client for resolved, or the release client itself
// when resolved names the same credential as the release: one client means
// one connection pool and one user agent, and it keeps the single-credential
// arrangement exactly as it was.
func splitClientFor(client *github.Client, resolved, token string) *github.Client {
	current, _ := plan.Token(token)
	if resolved == current {
		return client
	}
	split := github.New(resolved)
	split.UserAgent = client.UserAgent
	return split
}

// tapClientFor returns the client the tap is written with.
func tapClientFor(client *github.Client, tapToken, token string) *github.Client {
	value, _ := plan.TapToken(tapToken, token)
	return splitClientFor(client, value, token)
}

// releaseClientFor returns the client the GitHub release itself is created
// and published with. Mirrors tapClientFor exactly, one split credential at
// a time.
func releaseClientFor(client *github.Client, releaseToken, token string) *github.Client {
	value, _ := plan.ReleaseToken(releaseToken, token)
	return splitClientFor(client, value, token)
}

// publishTap writes a formula to the configured Homebrew tap, one per command
// the module builds.
//
// A module with several commands gets several formulas rather than a refusal:
// a formula installs one archive per platform, so `alpha` and `beta` cannot
// share one, and `Formula/alpha.rb` beside `Formula/beta.rb` is what a tap is
// shaped to hold anyway.
//
// info is what planAndBuild already read from the forge for the tap-files
// hook; passed in rather than read again so the description is asked for
// once, not once per publisher.
func publishTap(ctx context.Context, p *plan.Plan, result *release.Result, api brew.FileAPI, repo github.Repo, info *github.RepoInfo) error {
	if p.Tap == (github.Repo{}) {
		return nil
	}

	// Only a published release serves assets from the download URLs a formula
	// (or a tap-files plugin's output) names. Pointing a tap at a draft would
	// produce a cask or formula that resolves to a 404 for everyone but its
	// author.
	if p.Config.Draft {
		fmt.Println("  ! skipped the Homebrew tap: a draft release serves no public assets")
		return nil
	}

	// A prerelease's formula would overwrite the stable tap entry that
	// `brew install foo` still relies on. Until foo@next exists (a later
	// phase), a prerelease publishes nothing to the tap rather than clobber
	// it.
	if isPrerelease(p) {
		fmt.Println("  ! skipped the Homebrew tap: a prerelease must not overwrite the stable formula")
		return nil
	}

	if names := variantNames(p); len(names) > 0 {
		fmt.Printf("  ! no formula for variant %s: a variant's package is the repository's to choose\n",
			strings.Join(names, ", "))
	}

	for _, formula := range formulas(p, result, repo, info) {
		published, err := brew.Publish(ctx, api, p.Tap, formula)
		if err != nil {
			return err
		}
		fmt.Printf("  %s %s in %s\n", published.Status, published.Path, p.Tap)
	}

	// Written in the same tap update as the formula: whatever a tap-files
	// plugin rendered was already validated back when the release was built,
	// so nothing here can still fail on the plugin's account.
	for _, f := range result.TapFiles {
		published, err := brew.PublishFile(ctx, api, p.Tap, f.Path, []byte(f.Content),
			fmt.Sprintf("%s %s", p.Project, p.Version))
		if err != nil {
			return err
		}
		fmt.Printf("  %s %s in %s\n", published.Status, published.Path, p.Tap)
	}
	return nil
}

// variantNames are the variants this release built, in order.
func variantNames(p *plan.Plan) []string {
	names := make([]string, 0, len(p.Config.Variants))
	for _, v := range p.Config.Variants {
		names = append(names, v.Name)
	}
	return names
}

// variantArchives are the archive base names belonging to a variant.
func variantArchives(p *plan.Plan) map[string]bool {
	out := map[string]bool{}
	for _, g := range p.Groups {
		if g.Variant != "" {
			out[g.Name] = true
		}
	}
	return out
}

// formulas builds one formula per archive, grouping the artifacts by the
// archive they belong to.
//
// Per archive rather than per binary, because a formula names one archive per
// platform: an archive holding eleven tools is one formula that installs
// eleven binaries, not eleven formulas fighting over the same file.
//
// A variant's archives are left out. `brew` says where the release's formula
// goes, and a variant is a second product from the same source: a windowed
// build usually belongs in a cask rather than a formula, and writing one
// anyway would put a package in the tap that nobody asked for.
func formulas(p *plan.Plan, result *release.Result, repo github.Repo, info *github.RepoInfo) []brew.Formula {
	tag := releaseTag(p)

	order := make([]string, 0, len(p.Groups))
	platforms := map[string][]brew.Platform{}
	binaries := map[string][]string{}
	variants := variantArchives(p)

	for _, a := range result.Artifacts {
		name := formulaName(a, p.Version)
		if variants[name] {
			continue
		}
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
			Caveats:   p.Config.BrewCaveats,
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
