// Package selfupdate implements the `update` subcommand's core: find a
// release of the pinned repository on GitHub Releases, verify it with
// shared/go/releaseverify (the Sigstore bundle over checksums.txt from the
// release workflow of exactly that tag, the Rekor inclusion proof,
// release.json, the archive and the extracted binary, the no-downgrade
// watermark), and atomically replace the running executable.
//
// GitHub Releases is the only feed (Epic 35 story 35.1 AC9): the same one the
// API's update check reads, so the CLI and the server can never disagree
// about what a release is. MAKTABA_UPDATE_REPO retargets a fork and its
// signing identity together.
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

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
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

func (c *Client) get(ctx context.Context, path string, out any) error {
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
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func toRelease(r ghRelease) *Release {
	out := &Release{Tag: r.TagName, Prerelease: r.Prerelease, Assets: map[string]string{}}
	for _, a := range r.Assets {
		out.Assets[a.Name] = a.URL
	}
	return out
}

// Latest is the highest release of the channel.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	var rels []ghRelease
	if err := c.get(ctx, "/repos/"+c.o.Identity.Repo()+"/releases?per_page=30", &rels); err != nil {
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
	if err := c.get(ctx, "/repos/"+c.o.Identity.Repo()+"/releases/tags/"+tag, &r); err != nil {
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
		return releaseverify.EmbeddedTrustedRoot()
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
