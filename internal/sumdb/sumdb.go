// Package sumdb cross-checks a release's published source archive against
// sum.golang.org and the module proxy: see docs/hld/sumdb.md.
//
// Most users get a module's source through `go get`, which trusts
// sum.golang.org, not letsgo's own source archive. Nothing otherwise proves
// the two describe the same tree — a moved tag or an archive built from a
// different commit would go unnoticed. This package closes that gap by
// downloading the module proxy's zip, checking its hash against sumdb, and
// comparing it file by file with the archive actually published.
package sumdb

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/publish"
)

// DefaultURL is the public checksum database, matching Go's own default
// GOSUMDB.
const DefaultURL = "https://sum.golang.org"

// maxZipSize bounds the module zip fetched from the proxy. A module zip is
// input from the network, and one that claimed to grow without limit would
// otherwise be answered by filling memory.
const maxZipSize = 512 << 20

// sumdbRetryBudget bounds how long Check waits for sum.golang.org to publish
// a record before giving up and reporting NotFound rather than failing
// (SD-6): a moment after tagging, the record may simply not exist yet.
const sumdbRetryBudget = 60 * time.Second

// sumdbSleep is time.Sleep, replaced in tests so the retry loop runs fast.
var sumdbSleep = time.Sleep

// Result is what comparing a release's source archive against sum.golang.org
// and the module proxy found.
type Result struct {
	// Matched is true when sumdb and the proxy zip agree, and every file in
	// the zip is byte-identical to the same path in the source archive.
	Matched bool

	// SumH1 and ZipH1 are the h1: hash sumdb reported and the hash computed
	// from the proxy zip itself, the same way. They differ only if the
	// proxy and sumdb disagree with each other — nothing a single release
	// can cause on its own, but worth surfacing distinctly from a file
	// mismatch.
	SumH1, ZipH1 string

	// Mismatched names files present in both the zip and the archive whose
	// content differs.
	Mismatched []string

	// Missing names files the zip has that the archive does not.
	Missing []string

	// Extra names files the archive has that the zip does not. A module zip
	// can legitimately exclude files the archive carries, so this is
	// informational and never fails the check.
	Extra []string

	// NotFound is true when sum.golang.org still had no record for
	// module@version after retrying for sumdbRetryBudget (SD-6). Every
	// other field is zero in that case — Check never reached the proxy.
	NotFound bool

	// ProxyPending is true when the module proxy hadn't resolved
	// module@version to expectedCommit after retrying for
	// sumdbRetryBudget — the same moment-after-tagging lag SD-6 already
	// tolerates for sumdb's own lookup, extended to the proxy's own git
	// resolution: it answers module@version once, from whatever its
	// mirror of the VCS shows at that moment, and every endpoint for that
	// version is then cached forever, so asking too soon risks
	// permanently locking in the wrong commit's zip. Every other field is
	// zero in that case — Check never reached sumdb or the zip.
	ProxyPending bool
}

// Check fetches module@version's record from sumdb and its zip from proxy,
// then compares every file in the zip against the same path in the local
// source archive at archivePath (a tar.gz as internal/build.WriteSource
// produces, with every entry under one top-level directory).
//
// expectedCommit, when non-empty, is the git commit the release was
// actually built from: before ever touching sumdb or the zip, Check waits
// for the proxy to agree module@version resolves to that commit (see
// ProxyPending), so a laggy proxy is reported as "not caught up yet"
// instead of a false content mismatch. Pass "" to skip that wait.
func Check(ctx context.Context, sumdbURL, proxyURL, modulePath, version, archivePath, expectedCommit string) (Result, error) {
	version = normalizeVersion(version)

	consistent, err := awaitProxyCommit(ctx, proxyURL, modulePath, version, expectedCommit)
	if err != nil {
		return Result{}, err
	}
	if !consistent {
		return Result{ProxyPending: true}, nil
	}

	sumH1, found, err := lookupHashWithRetry(ctx, sumdbURL, modulePath, version)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{NotFound: true}, nil
	}

	zipData, err := fetchZip(ctx, proxyURL, modulePath, version)
	if err != nil {
		return Result{}, err
	}

	zipFiles, zipH1, err := hashZip(zipData, modulePath, version)
	if err != nil {
		return Result{}, err
	}

	archiveFiles, err := readArchive(archivePath)
	if err != nil {
		return Result{}, err
	}

	result := Result{SumH1: sumH1, ZipH1: zipH1}

	seen := make(map[string]bool, len(zipFiles))
	for name, data := range zipFiles {
		seen[name] = true
		switch archived, ok := archiveFiles[name]; {
		case !ok:
			result.Missing = append(result.Missing, name)
		case !bytes.Equal(archived, data):
			result.Mismatched = append(result.Mismatched, name)
		}
	}
	for name := range archiveFiles {
		if !seen[name] {
			result.Extra = append(result.Extra, name)
		}
	}
	sort.Strings(result.Missing)
	sort.Strings(result.Mismatched)
	sort.Strings(result.Extra)

	result.Matched = sumH1 == zipH1 && len(result.Missing) == 0 && len(result.Mismatched) == 0
	return result, nil
}

func normalizeVersion(version string) string {
	if !strings.HasPrefix(version, "v") {
		return "v" + version
	}
	return version
}

// retryUntil calls attempt repeatedly with exponential backoff (1s, 2s,
// 4s, ...) until it reports done or a genuine error, giving up once the
// next sleep would push total elapsed time past sumdbRetryBudget. Shared by
// lookupHashWithRetry (SD-6: sumdb may not have a record yet) and
// awaitProxyCommit (the module proxy's own git resolution can lag the same
// way, moments after tagging).
func retryUntil(attempt func() (done bool, err error)) error {
	backoff := time.Second
	var elapsed time.Duration
	for {
		done, err := attempt()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if elapsed+backoff > sumdbRetryBudget {
			return nil
		}
		sumdbSleep(backoff)
		elapsed += backoff
		backoff *= 2
	}
}

// lookupHashWithRetry calls lookupHashOnce, retrying with backoff while
// sumdb has no record yet, up to sumdbRetryBudget (SD-6): a moment after
// tagging, the record may simply not exist yet. found is false only when
// the budget is exhausted with the record still missing — never on a
// genuine error, which is returned instead.
func lookupHashWithRetry(ctx context.Context, sumdbURL, modulePath, version string) (h1 string, found bool, err error) {
	err = retryUntil(func() (bool, error) {
		var notFound bool
		var attemptErr error
		h1, notFound, attemptErr = lookupHashOnce(ctx, sumdbURL, modulePath, version)
		if attemptErr != nil {
			return false, attemptErr
		}
		found = !notFound
		return found, nil
	})
	return h1, found, err
}

// awaitProxyCommit retries the module proxy's .info endpoint until it
// reports expectedCommit as module@version's origin commit, up to
// sumdbRetryBudget. expectedCommit == "" (the caller has no commit to
// verify against) always reports consistent. A proxy response with no
// Origin field (a non-git VCS, or an implementation that doesn't report
// one — Origin is proxy.golang.org's own extension, not part of the base
// protocol) can't be verified either, and is likewise treated as
// consistent rather than blocked on forever.
func awaitProxyCommit(ctx context.Context, proxyURL, modulePath, version, expectedCommit string) (consistent bool, err error) {
	if expectedCommit == "" {
		return true, nil
	}
	err = retryUntil(func() (bool, error) {
		commit, attemptErr := proxyOrigin(ctx, proxyURL, modulePath, version)
		if attemptErr != nil {
			return false, attemptErr
		}
		consistent = commit == "" || commit == expectedCommit
		return consistent, nil
	})
	return consistent, err
}

// proxyOrigin fetches module@version's .info document from the proxy and
// returns the commit its Origin recorded, or "" if the document carried no
// Origin (or didn't parse as JSON at all) — left for the caller to treat as
// "can't verify" rather than a hard error.
func proxyOrigin(ctx context.Context, proxyURL, modulePath, version string) (commit string, err error) {
	url := fmt.Sprintf("%s/%s/@v/%s.info", strings.TrimSuffix(proxyURL, "/"),
		publish.EscapeModulePath(modulePath), publish.EscapeModulePath(version))

	body, status, err := get(ctx, url, 1<<16)
	if err != nil {
		return "", err
	}
	if status < 200 || status > 299 {
		return "", fmt.Errorf("sumdb: fetching %s: status %d", url, status)
	}

	var info struct {
		Origin struct {
			Hash string
		}
	}
	if json.Unmarshal(body, &info) != nil {
		return "", nil //nolint:nilerr // an unparseable .info body can't be verified, not a hard error
	}
	return info.Origin.Hash, nil
}

// lookupHashOnce fetches sumdb's lookup record for module@version and
// returns the h1: line for the module zip (not the /go.mod line). notFound
// is true only when sumdb responded 404 (no record yet); any other
// non-2xx status or transport failure is a hard error.
func lookupHashOnce(ctx context.Context, sumdbURL, modulePath, version string) (h1 string, notFound bool, err error) {
	url := fmt.Sprintf("%s/lookup/%s@%s", strings.TrimSuffix(sumdbURL, "/"),
		publish.EscapeModulePath(modulePath), publish.EscapeModulePath(version))

	body, status, err := get(ctx, url, 1<<20)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", true, nil
	}
	if status < 200 || status > 299 {
		return "", false, fmt.Errorf("sumdb: fetching %s: status %d", url, status)
	}

	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[1] == version && strings.HasPrefix(fields[2], "h1:") {
			return fields[2], false, nil
		}
	}
	return "", false, fmt.Errorf("sumdb: no h1 hash for %s@%s in sumdb's response", modulePath, version)
}

// fetchZip downloads the module proxy's zip for module@version.
func fetchZip(ctx context.Context, proxyURL, modulePath, version string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/@v/%s.zip", strings.TrimSuffix(proxyURL, "/"),
		publish.EscapeModulePath(modulePath), publish.EscapeModulePath(version))
	data, status, err := get(ctx, url, maxZipSize)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("sumdb: fetching %s: status %d", url, status)
	}
	return data, nil
}

// get performs a GET request, returning the response body and status code.
// err is non-nil only for a request-building or transport-level failure;
// callers decide how to treat a non-2xx status themselves.
func get(ctx context.Context, url string, limit int64) (data []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("sumdb: %w", err)
	}
	req.Header.Set("User-Agent", "letsgo")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("sumdb: fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, 0, fmt.Errorf("sumdb: reading %s: %w", url, err)
	}
	return data, resp.StatusCode, nil
}

// hashZip reads every entry of a module zip, keyed by its path relative to
// the zip's own "<module>@<version>/" prefix, and computes the zip's h1:
// hash the same way sum.golang.org does (golang.org/x/mod/sumdb/dirhash's
// Hash1: each file's own sha256, formatted "<hex>  <name>\n" over every
// entry sorted by its full name, then the sha256 of that, base64-encoded).
// letsgo has no dependencies to import that algorithm from, so it is
// reproduced here.
func hashZip(data []byte, modulePath, version string) (files map[string][]byte, h1 string, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", fmt.Errorf("sumdb: reading the module zip: %w", err)
	}

	prefix := modulePath + "@" + version + "/"

	type entry struct {
		name string
		sum  [sha256.Size]byte
	}
	entries := make([]entry, 0, len(zr.File))
	files = make(map[string][]byte, len(zr.File))

	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("sumdb: reading %s from the module zip: %w", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, "", fmt.Errorf("sumdb: reading %s from the module zip: %w", f.Name, err)
		}

		entries = append(entries, entry{name: f.Name, sum: sha256.Sum256(content)})
		if rel, ok := strings.CutPrefix(f.Name, prefix); ok {
			files[rel] = content
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%x  %s\n", e.sum, e.name)
	}
	return files, "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
