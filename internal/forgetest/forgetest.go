// Package forgetest serves a letsgo-published release the way a forge does.
//
// Two packages need this shape: selfupdate, which reads a release to replace a
// running binary, and the plugin command, which reads one to install a helper
// program. Both were modelling "what a GitHub release published by letsgo
// looks like" separately, and two models of that can disagree — at which point
// one package's tests pass against a release the other would reject.
//
// The archives are built by letsgo's own archive writer rather than by a
// hand-rolled tar stream, so a fixture cannot drift from the format the tool
// actually publishes.
package forgetest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// modTime is fixed so that an archive is a function of its contents. It is
// deliberately after 1980, which is the earliest a zip's DOS timestamp can
// represent.
var modTime = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)

// Notes is the release body a forge serves unless told otherwise. A program
// may want to show what changed before replacing itself, so there has to be
// something there to show.
const Notes = "### Features\n\n- something"

// Forge is a running fake forge holding one release.
//
// Manifest and Archives are exported because the interesting tests are the
// ones that corrupt them: a release with no manifest, an archive that does not
// match its recorded digest, an artifact described but never attached.
type Forge struct {
	Repo string
	Tag  string
	Body string

	// Manifest is the letsgo.json served with the release. A nil manifest is a
	// release letsgo did not publish, and the asset is then absent.
	Manifest []byte

	// Archives maps an asset name to its bytes.
	Archives map[string][]byte

	server *httptest.Server
}

// New starts a forge serving one release of repo at tag, with nothing
// published into it yet.
func New(t testing.TB, repo, tag string) *Forge {
	t.Helper()

	f := &Forge{Repo: repo, Tag: tag, Body: Notes, Archives: map[string][]byte{}}

	release := func(w http.ResponseWriter, _ *http.Request) {
		var assets []map[string]string
		// Only a release published by letsgo carries a manifest.
		if f.Manifest != nil {
			assets = append(assets, f.asset(manifest.FileName))
		}
		for name := range f.Archives {
			assets = append(assets, f.asset(name))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.Tag,
			"body":     f.Body,
			"html_url": "https://example.test/releases/" + f.Tag,
			"assets":   assets,
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+repo+"/releases/latest", release)
	mux.HandleFunc("/repos/"+repo+"/releases/tags/"+tag, release)
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/download/")
		if name == manifest.FileName {
			_, _ = w.Write(f.Manifest)
			return
		}
		content, ok := f.Archives[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(content)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *Forge) asset(name string) map[string]string {
	return map[string]string{
		"name":                 name,
		"browser_download_url": f.URL() + "/download/" + name,
	}
}

// URL is the forge's API endpoint.
func (f *Forge) URL() string { return f.server.URL }

// Options are the selfupdate options pointing at this forge, for linux/amd64.
// The caller sets Current, Binary or Tag on top.
func (f *Forge) Options() selfupdate.Options {
	return selfupdate.Options{
		Repo:        f.Repo,
		APIEndpoint: f.URL(),
		OS:          "linux",
		Arch:        "amd64",
	}
}

// Publish publishes a linux/amd64 release holding one executable.
func (f *Forge) Publish(t testing.TB, project, version, binary, content string) {
	t.Helper()

	name := fmt.Sprintf("%s_%s_linux_amd64.tar.gz", project, version)
	data := TarGz(t, binary, content)
	f.Archives[name] = data

	f.SetManifest(t, &manifest.Manifest{
		Schema: manifest.Schema, Project: project, Version: version, Tag: f.Tag,
		Artifacts: []manifest.Artifact{{
			Name: name, OS: "linux", Arch: "amd64", Binary: binary,
			SHA256:       Sum(data),
			BinarySHA256: Sum([]byte(content)),
		}},
	})
}

// PublishCommands publishes a release the way a repository with several
// commands does: one archive per command per platform, so that os and arch
// alone no longer say which artifact is meant.
//
// Each binary's content is its own name plus " bytes", so a test can tell
// which one it got.
func (f *Forge) PublishCommands(t testing.TB, project, version string, binaries ...string) {
	t.Helper()

	m := &manifest.Manifest{
		Schema: manifest.Schema, Project: project, Version: version, Tag: f.Tag,
	}
	for _, binary := range binaries {
		name := fmt.Sprintf("%s_%s_linux_amd64.tar.gz", binary, version)
		content := Content(binary)
		data := TarGz(t, binary, content)
		f.Archives[name] = data

		m.Artifacts = append(m.Artifacts, manifest.Artifact{
			Name: name, OS: "linux", Arch: "amd64", Binary: binary,
			SHA256:       Sum(data),
			BinarySHA256: Sum([]byte(content)),
		})
	}
	f.SetManifest(t, m)
}

// PublishZip publishes a Windows release: a zip, and a binary carrying the
// .exe the manifest does not record but the archive does.
func (f *Forge) PublishZip(t testing.TB, project, version, binary, content string) {
	t.Helper()

	name := fmt.Sprintf("%s_%s_windows_amd64.zip", project, version)
	data := Zip(t, binary+".exe", content)
	f.Archives[name] = data

	f.SetManifest(t, &manifest.Manifest{
		Schema: manifest.Schema, Project: project, Version: version, Tag: f.Tag,
		Artifacts: []manifest.Artifact{{
			Name: name, OS: "windows", Arch: "amd64", Binary: binary,
			SHA256:       Sum(data),
			BinarySHA256: Sum([]byte(content)),
		}},
	})
}

// SetManifest encodes m and serves it as the release's letsgo.json.
func (f *Forge) SetManifest(t testing.TB, m *manifest.Manifest) {
	t.Helper()

	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f.Manifest = data
}

// DecodeManifest reads back the manifest currently being served, for a test
// that wants to corrupt one field of it.
func (f *Forge) DecodeManifest(t testing.TB) *manifest.Manifest {
	t.Helper()

	var m manifest.Manifest
	if err := json.Unmarshal(f.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// Content is the body given to a published binary of this name.
func Content(binary string) string { return binary + " bytes" }

// TarGz builds a release archive holding one executable.
func TarGz(t testing.TB, name, content string) []byte {
	t.Helper()
	return write(t, archive.FormatTarGz, name, content)
}

// Zip builds a Windows-shaped release archive.
func Zip(t testing.TB, name, content string) []byte {
	t.Helper()
	return write(t, archive.FormatZip, name, content)
}

// write builds the archive through letsgo's own writer, so a fixture is made
// by the code that makes the real thing.
func write(t testing.TB, format archive.Format, name, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	entries := []archive.Entry{archive.FromBytes(name, true, []byte(content))}
	if err := archive.Write(&buf, format, entries, modTime); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Sum is the hex SHA-256 of data, as the manifest records digests.
func Sum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
