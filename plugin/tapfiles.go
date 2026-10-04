package plugin

import (
	"net/url"
	"strings"

	"github.com/danielriddell21/letsgo/manifest"
)

// DownloadURL is where a release asset is served: the forge's download URL
// for the asset called name on the release tagged tag, in repo ("owner/name").
//
// The tag is escaped per path segment, not as one component: a scoped
// release's tag carries a literal "/", and the forge's own route treats that
// as directory segments. Encoding it to %2F would ask for a tag that does not
// exist.
func DownloadURL(repo, tag, name string) string {
	return "https://github.com/" + repo + "/releases/download/" + escapePath(tag) + "/" + url.PathEscape(name)
}

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// TapFilesInputFromManifest rebuilds the tap-files hook's input from a
// previously published release's manifest, for yank and for a Plugin run on
// its own: the Plugin is asked the same question about a release that already
// happened. repo and tap are "owner/name".
//
// Unlike a fresh release, there is no repository description or licence to
// carry: the input has no place for them here, and answering should not need
// the forge for bytes already published. ConfigDir is left for the caller.
func TapFilesInputFromManifest(m *manifest.Manifest, repo, tap, caveats string) TapFilesInput {
	tag := m.Tag
	if tag == "" {
		tag = "v" + m.Version
	}

	in := TapFilesInput{
		Project:   m.Project,
		Version:   m.Version,
		Tag:       tag,
		Repo:      repo,
		Tap:       tap,
		Homepage:  "https://" + repo,
		Caveats:   caveats,
		Artifacts: make([]TapArtifact, 0, len(m.Artifacts)),
	}

	for _, a := range m.Artifacts {
		in.Artifacts = append(in.Artifacts, TapArtifact{
			Archive:  a.Name,
			Variant:  a.Variant,
			OS:       a.OS,
			Arch:     a.Arch,
			SHA256:   a.SHA256,
			URL:      DownloadURL(repo, tag, a.Name),
			Binaries: a.BinaryNames(),
		})
	}
	return in
}
