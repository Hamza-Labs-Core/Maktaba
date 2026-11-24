// Package selfupdate implements the `update` subcommand's core: find a
// release of the pinned repository on GitHub Releases, verify it with
// shared/go/releaseverify (the Sigstore bundle over checksums.txt from the
// release workflow of exactly that tag, the Rekor inclusion proof,
// release.json, the archive and the extracted binary, the no-downgrade
// watermark), and atomically replace the running executable.
//
// The newest release is found as the server finds it: in this platform's
// release feed (releaseverify.VerifyFeed, signed by update-feed.yml; a pulled
// release is never chosen), and in GitHub's list only when the feed cannot be
// reached. The files are always GitHub Releases' and verified the same way
// (Epic 35 story 35.1 AC9), so the CLI and the server can never disagree about
// what a release is. MAKTABA_UPDATE_REPO retargets a fork and its signing
// identity together (a fork reads its GitHub list: the feed is the default
// repository's).
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
)

// DefaultAPIBase is the GitHub REST API.
const DefaultAPIBase = "https://api.github.com"

// Options configure a Client. Zero values are the production defaults.
type Options struct {
	// Identity is the repository and signer; zero is releaseverify.DefaultRepo.
	Identity releaseverify.Identity
	// Channel is "stable" (no prereleases) or "beta".
	Channel string
	// Token is an optional GitHub token (rate limit).
	Token string
	// HTTP, APIBase, Platform and Trusted are test seams.
	HTTP     *http.Client
	APIBase  string
	Platform string
	// Trusted returns the Sigstore trust root; nil refreshes a RootStore in
	// the user cache directory and falls back to the embedded root.
	Trusted func(ctx context.Context) (root.TrustedMaterial, error)
	// FeedURL is the release feed read before GitHub's list: "" is
	// releaseverify.DefaultFeedURL for the default repository with the real API
	// (none for a fork or a test's APIBase, none when MAKTABA_UPDATE_FEED=off),
	// "off" none. FeedDownloadBase is where a test feed's files are.
	FeedURL          string
	FeedDownloadBase string
}

// Client talks to GitHub Releases and verifies what it downloads.
type Client struct{ o Options }

// New returns a Client.
func New(o Options) *Client {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.APIBase == "" {
		o.APIBase = DefaultAPIBase
	}
	o.APIBase = strings.TrimRight(o.APIBase, "/")
	if o.Channel == "" {
		o.Channel = "stable"
	}
	if o.Trusted == nil {
		o.Trusted = userRootStore
	}
	switch {
	case strings.EqualFold(o.FeedURL, "off") || strings.EqualFold(strings.TrimSpace(os.Getenv("MAKTABA_UPDATE_FEED")), "off"):
		o.FeedURL = ""
	case o.FeedURL == "" && o.APIBase == DefaultAPIBase && o.Identity.Repo() == releaseverify.DefaultRepo:
		o.FeedURL = releaseverify.DefaultFeedURL
	}
	o.FeedURL = strings.TrimRight(o.FeedURL, "/")
	return &Client{o: o}
}

// Release is one GitHub release of the pinned repository.
type Release struct {
	Tag        string
	Prerelease bool
	Assets     map[string]string // asset name → download URL
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// ErrNoRelease: nothing matches.
var ErrNoRelease = errors.New("no such release")

// What the client reads from GitHub is held to these caps, each checked against the real
// list (tests/fixtures/github-releases) with room for the releases' assets to double in
// under 70% of it. A release is some 330 KB as GitHub lists it today (some 190 assets): the
// list asks for listPerPage of them, some 3.3 MB, against listLimit; one release by its tag
// against releaseLimit; each small signed file (checksums.txt, its Sigstore bundle,
// release.json: some 15 KB at most) against smallFileLimit. A page of 30 passed the old
// 8 MiB on 2026-11-23, and every look for an update failed from then on.
const (
	listPerPage    = 10
	listLimit      = 16 << 20
	releaseLimit   = 8 << 20
	smallFileLimit = 4 << 20
)

// errTooLarge is an answer longer than its cap.
var errTooLarge = errors.New("larger than its cap")

func (c *Client) get(ctx context.Context, path string, limit int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.o.APIBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.o.Token)
	}
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github releases %s: HTTP %d", path, resp.StatusCode)
	}
	// one byte past the cap tells an answer that is too long from one that does not decode
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("github releases %s: %w", path, err)
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("github releases %s: larger than %d bytes: %w", path, limit, errTooLarge)
	}
	return json.Unmarshal(b, out)
}

func toRelease(r ghRelease) *Release {
	out := &Release{Tag: r.TagName, Prerelease: r.Prerelease, Assets: map[string]string{}}
	for _, a := range r.Assets {
		out.Assets[a.Name] = a.URL
	}
	return out
}

// Latest is the highest release of the channel: the feed's, or GitHub's list's when the
// feed cannot be reached. A feed that is reached and fails its checks is an error.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	if c.o.FeedURL != "" {
		rel, err := c.feedLatest(ctx)
		if !errors.Is(err, errFeedUnreachable) {
			return rel, err
		}
	}
	var rels []ghRelease
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/releases?per_page=%d", c.o.Identity.Repo(), listPerPage), listLimit, &rels); err != nil {
		return nil, err
	}
	var best *ghRelease
	for i := range rels {
		r := &rels[i]
		if r.Draft || !releaseverify.ValidTag(r.TagName) {
			continue
		}
		if c.o.Channel != "beta" && (r.Prerelease || strings.Contains(r.TagName, "-")) {
			continue
		}
		if best == nil || releaseverify.CompareVersions(r.TagName, best.TagName) > 0 {
			best = r
		}
	}
	if best == nil {
		return nil, ErrNoRelease
	}
	return toRelease(*best), nil
}

// ByTag is one release ("v1.5.0" or "1.5.0").
func (c *Client) ByTag(ctx context.Context, version string) (*Release, error) {
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !releaseverify.ValidTag(tag) {
		return nil, fmt.Errorf("invalid version %q", version)
	}
	var r ghRelease
	if err := c.get(ctx, "/repos/"+c.o.Identity.Repo()+"/releases/tags/"+tag, releaseLimit, &r); err != nil {
		return nil, err
	}
	if r.Draft {
		return nil, ErrNoRelease
	}
	return toRelease(r), nil
}

// Prepared is a verified update ready to install.
type Prepared struct {
	Update *releaseverify.ServerUpdate
	Self   string
}

// Prepare downloads and verifies rel for the executable at self and applies
// the watermark rules. It writes nothing.
func (c *Client) Prepare(ctx context.Context, rel *Release, self string) (*Prepared, error) {
	tm, err := c.o.Trusted(ctx)
	if err != nil {
		return nil, err
	}
	up, err := releaseverify.FetchServer(ctx, releaseverify.HTTPDownloader(c.o.HTTP),
		releaseverify.Input{Tag: rel.Tag, Identity: c.o.Identity, TrustedMaterial: tm}, rel.Assets, c.o.Platform)
	if err != nil {
		return nil, err
	}
	w, err := releaseverify.WatermarkFile{Path: releaseverify.FlatWatermarkPath(self)}.Load()
	if err != nil {
		return nil, fmt.Errorf("read watermark: %w", err)
	}
	if err := releaseverify.CheckMonotonic(w, up.Result); err != nil {
		return nil, err
	}
	return &Prepared{Update: up, Self: self}, nil
}

// Install swaps the verified binary in (keeping <self>.bak for Rollback) and
// raises the watermark.
func (p *Prepared) Install() error {
	if err := replaceExecutable(p.Self, p.Update.Binary); err != nil {
		return err
	}
	return releaseverify.WatermarkFile{Path: releaseverify.FlatWatermarkPath(p.Self)}.Commit(p.Update.Result)
}

// Self is the running executable, symlinks resolved.
func Self() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate self: %w", err)
	}
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	return self, nil
}

// Rollback restores <self>.bak, the release that ran before the last update.
// It is the only way to run a version below the watermark, and it does not
// lower the watermark (Epic 35 design §3.5).
func Rollback(self string) error {
	bak := self + ".bak"
	b, err := os.ReadFile(bak)
	if err != nil {
		return fmt.Errorf("no previous release kept at %s: %w", bak, err)
	}
	return replaceExecutable(self, b)
}

// userRootStore refreshes the Sigstore trusted root through TUF into the
// user cache directory (the update needs the network anyway) and serves the
// newest good root; a failed refresh keeps the cached or embedded one.
func userRootStore(ctx context.Context) (root.TrustedMaterial, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		// No cache directory ($HOME unset under a service manager): the
		// embedded root, still held to the age limit.
		tm, _, err := (&releaseverify.RootStore{}).TrustedMaterial()
		return tm, err
	}
	s := &releaseverify.RootStore{Dir: filepath.Join(dir, "maktaba", "sigstore")}
	if err := s.Refresh(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: Sigstore trusted root refresh failed (%v); using the last good root\n", err)
	}
	tm, _, err := s.TrustedMaterial()
	return tm, err
}

// replaceExecutable swaps the new bytes in for the target. It writes the new
// binary next to the target, moves the live one to <target>.bak (Windows can
// rename a running .exe but not delete it) and renames the new one into
// place; each rename is atomic within a directory.
func replaceExecutable(target string, body []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".maktaba-server.new.*")
	if err != nil {
		return fmt.Errorf("temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}
	bak := target + ".bak"
	prev := bak + ".prev"
	_ = os.Remove(prev)
	if _, err := os.Stat(bak); err == nil {
		_ = os.Rename(bak, prev) // keep the old .bak until the swap is done
	}
	if err := os.Rename(target, bak); err != nil {
		_ = os.Rename(prev, bak)
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Rename(bak, target) // roll back so the install keeps a binary
		_ = os.Rename(prev, bak)
		return fmt.Errorf("move new binary into place: %w", err)
	}
	_ = os.Remove(prev)
	return nil
}

// Newer reports whether a is a strictly newer release than b.
func Newer(a, b string) bool { return releaseverify.CompareVersions(a, b) > 0 }

// Stage verifies rel and stages it side by side under the layout l
// (bin/releases/<v>/, story 35.2): the install flow of a self-managed machine,
// shared with the API's self-update and the orchestrator
// (releaseverify/install). It changes no pointer; Activate does.
func (c *Client) Stage(ctx context.Context, rel *Release, l layout.Layout) (*install.Staged, error) {
	tm, err := c.o.Trusted(ctx)
	if err != nil {
		return nil, err
	}
	return install.Stage(ctx, l, releaseverify.HTTPDownloader(c.o.HTTP),
		releaseverify.Input{Tag: rel.Tag, Identity: c.o.Identity, TrustedMaterial: tm}, rel.Assets, c.o.Platform)
}

// maxAsset bounds a release asset FetchAsset writes to disk (the Linux
// payload tarball is a few hundred MB).
const maxAsset = 2 << 30

// FetchAsset verifies rel (the Sigstore bundle over checksums.txt from the
// release workflow of exactly that tag, its Rekor inclusion proof,
// release.json) and downloads its asset name into dir, checked against
// checksums.txt. It returns the file and what the verification proved; a
// file that fails the check is removed.
func (c *Client) FetchAsset(ctx context.Context, rel *Release, name, dir string) (string, *releaseverify.Result, error) {
	tm, err := c.o.Trusted(ctx)
	if err != nil {
		return "", nil, err
	}
	get := releaseverify.HTTPDownloader(c.o.HTTP)
	small := func(n string) ([]byte, error) {
		u, ok := rel.Assets[n]
		if !ok {
			return nil, nil
		}
		b, err := get(ctx, u, smallFileLimit)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", n, err)
		}
		return b, nil
	}
	in := releaseverify.Input{Tag: rel.Tag, Identity: c.o.Identity, TrustedMaterial: tm}
	if in.Checksums, err = small(releaseverify.ChecksumsFile); err != nil {
		return "", nil, err
	}
	if in.Bundle, err = small(releaseverify.BundleFile); err != nil {
		return "", nil, err
	}
	if in.Manifest, err = small(releaseverify.ManifestFile); err != nil {
		return "", nil, err
	}
	res, err := releaseverify.Verify(in)
	if err != nil {
		return "", nil, err
	}
	u, ok := rel.Assets[name]
	if !ok {
		return "", nil, fmt.Errorf("%w: %s has no %s", releaseverify.ErrNotFound, rel.Tag, name)
	}
	path := filepath.Join(dir, name)
	if err := c.download(ctx, u, path); err != nil {
		_ = os.Remove(path)
		return "", nil, fmt.Errorf("download %s: %w", name, err)
	}
	if err := res.VerifyArtifact(releaseverify.Artifact{Name: name, Path: path}); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	return path, res, nil
}

// download streams url into a new file at path.
func (c *Client) download(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxAsset+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxAsset {
		err = fmt.Errorf("GET %s: larger than %d bytes", url, int64(maxAsset))
	}
	return err
}
