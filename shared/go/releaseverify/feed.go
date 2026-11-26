package releaseverify

// The release feed: how a machine finds the releases it can install without reading
// GitHub's list of every release (2026-11-23: that list had grown to 10.5 MB for 30
// releases of some 190 files each, past every home's cap, and no home saw a release
// for a day).
//
// update-feed.yml writes, after each release is published and verified, one small
// file per channel and platform:
//
//	<DefaultFeedURL>/<channel>/<platform>.json              the feed (Feed)
//	<DefaultFeedURL>/<channel>/<platform>.json.sigstore.json its Sigstore bundle
//	<DefaultFeedURL>/<channel>/index.json (+ .sigstore.json) what the channel has (FeedIndex)
//
// Each feed lists the newest FeedKeep releases of its channel, each with only the files
// a machine of its platform installs (FeedFileNames), and a pulled release marked
// withdrawn. It is signed keyless like a release, by update-feed.yml on main
// (Identity.FeedSAN); VerifyFeed checks that signature and everything the feed says
// before a machine believes any of it. The files themselves stay on GitHub Releases and
// are verified as always (Verify): the feed only says which releases there are.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
)

const (
	// DefaultFeedURL is where the feed is served: static files of maktaba.tv's Worker
	// (web/packages/website), deployed by website-deploy.yml.
	DefaultFeedURL = "https://maktaba.tv/updates/v1"
	// FeedSchema is the feed's "schema"; a different one is refused (a new shape gets a
	// new path, /updates/v2).
	FeedSchema = 1
	// MaxFeed caps a feed or index file a machine reads (a real one is tens of KiB).
	MaxFeed = 1 << 20
	// FeedKeep is how many releases a feed lists that a machine may install.
	FeedKeep = 10
	// FeedFreshAge is how old a signed feed still counts as a fresh read: update-feed.yml
	// signs it every week, so one signed more than eight days ago is still read (up to
	// FeedMaxAge), but does not refresh the home's last fresh check — the home checks
	// GitHub's list too, or waits before installing automatically.
	FeedFreshAge = 8 * 24 * time.Hour
	// FeedMaxAge is how old a signed feed a machine believes: update-feed.yml signs it
	// again every week, so one older than this is an old feed served again (a machine then
	// reads GitHub's list instead).
	FeedMaxAge = 21 * 24 * time.Hour
	// FeedBundleSuffix names a feed file's Sigstore bundle (<file>.sigstore.json).
	FeedBundleSuffix = ".sigstore.json"
	// FeedIndexFile is each channel's index.
	FeedIndexFile = "index.json"
	// DefaultDownloadBase is where release files are downloaded from (GitHub Releases).
	DefaultDownloadBase = "https://github.com"
	// maxFeedReleases bounds what a feed may list, whatever it says.
	maxFeedReleases = 100
)

// Channels a feed is written for: stable lists stable releases, beta every release.
const (
	FeedStable = "stable"
	FeedBeta   = "beta"
)

// Feed is one channel's and platform's feed file.
type Feed struct {
	Schema      int           `json:"schema"`
	Repo        string        `json:"repo"`
	Channel     string        `json:"channel"`
	Platform    string        `json:"platform"` // "linux-amd64" (FeedPlatform of release.json's key)
	GeneratedAt time.Time     `json:"generated_at"`
	Releases    []FeedRelease `json:"releases"` // newest first
}

// FeedRelease is one release in a feed.
type FeedRelease struct {
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	Prerelease  bool      `json:"prerelease,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	URL         string    `json:"url"` // the release's page
	// ChecksumsSHA256 is the digest of its signed checksums.txt (the replay key a machine
	// records); ManifestSHA256 that of its release.json. Both files and the Sigstore
	// bundle are in Assets.
	ChecksumsSHA256 string `json:"checksums_sha256"`
	ManifestSHA256  string `json:"release_json_sha256"`
	// MigrationsSetSHA256 is release.json's migrations.set_sha256 (absent before it).
	MigrationsSetSHA256 string `json:"migrations_set_sha256,omitempty"`
	// Withdrawn: the release was pulled; a machine never installs it.
	Withdrawn *FeedWithdrawn `json:"withdrawn,omitempty"`
	Assets    []FeedAsset    `json:"assets"`
}

// FeedWithdrawn says why a release was pulled.
type FeedWithdrawn struct {
	Reason string `json:"reason"`
}

// FeedAsset is one release file. SHA256 is checksums.txt's digest (or the file's own,
// for checksums.txt and its bundle); it is absent for a file outside the signed set (the
// apps' latest.json, which their updater verifies itself).
type FeedAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// Asset returns the named file of r.
func (r FeedRelease) Asset(name string) (FeedAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return FeedAsset{}, false
}

// FeedPlatform is release.json's platform key ("linux/amd64") as a feed names it
// ("linux-amd64").
func FeedPlatform(p string) string { return strings.ReplaceAll(p, "/", "-") }

// FeedPath is a feed file's path under the feed's base ("stable/linux-amd64.json").
func FeedPath(channel, platform string) string { return channel + "/" + platform + ".json" }

// ReleaseDownloadURL is a release file's download address on base (GitHub's, or a
// test's): <base>/<repo>/releases/download/<tag>/<name>.
func ReleaseDownloadURL(base, repo, tag, name string) string {
	if base == "" {
		base = DefaultDownloadBase
	}
	return strings.TrimRight(base, "/") + "/" + repo + "/releases/download/" + tag + "/" + name
}

// FeedFileNames are the files a machine of platform (release.json's "os/arch") installs
// from a release, all of them by name from what the release lists: the three signed
// files; its server archive; the pipeline's wheel and constraints; the migrations; the
// component index; on Linux the runtime payload (the Linux app, a headless or a Docker
// install); and the apps' updater feed (latest.json), which the home hands to the Mac
// and Windows apps. Packs and first-party plugins are not listed: a machine fetches them
// by their own index. ok is false when the release has no archive for platform.
func FeedFileNames(m *Manifest, tag, platform string, sums Checksums) (names []string, ok bool) {
	a, has := m.Assets[platform]
	if !has || a.Archive == "" {
		return nil, false
	}
	names = []string{ChecksumsFile, BundleFile, ManifestFile, a.Archive}
	if p := m.Pipeline; p != nil {
		// every set release.json names: a layout install fetches each (venv.go)
		names = append(names, p.Wheel, p.Constraints, p.ConstraintsCUDA)
	}
	ver := strings.TrimPrefix(tag, "v")
	// the pipeline's other constraints sets (constraints-core, -stt), when the release has them
	for n := range sums {
		if strings.HasPrefix(n, "maktaba-pipeline-"+ver+"-constraints-") && strings.HasSuffix(n, ".txt") {
			names = append(names, n)
		}
	}
	if m.Migrations != nil && m.Migrations.Archive != "" {
		names = append(names, m.Migrations.Archive)
	}
	if m.Components != nil && m.Components.Index != "" {
		names = append(names, m.Components.Index)
	}
	if os, arch, _ := strings.Cut(platform, "/"); os == "linux" {
		names = append(names, "maktaba-linux-payload-"+ver+"-"+map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]+".tar.gz")
	}
	switch {
	case strings.HasPrefix(platform, "darwin/"), strings.HasPrefix(platform, "windows/"), strings.HasPrefix(platform, "linux/"):
		names = append(names, "latest.json")
	}
	seen := map[string]bool{}
	out := names[:0]
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out[4:])
	return out, true
}

// FeedSource is one published release as the feed's builder reads it: GitHub's listing
// of it and what Verify proved of its signed files.
type FeedSource struct {
	Tag          string
	Prerelease   bool
	PublishedAt  time.Time
	URL          string
	Assets       []FeedSourceAsset
	Verified     *Result // Verify of its checksums.txt, bundle and release.json
	BundleSHA256 string  // the digest of its checksums.txt.sigstore.json
}

// FeedSourceAsset is one file as GitHub lists it.
type FeedSourceAsset struct {
	Name string
	URL  string
	Size int64
}

// GHReleaseView is one release as `gh release view --json
// tagName,isDraft,isPrerelease,publishedAt,url,assets` prints it (update-feed.yml's input).
type GHReleaseView struct {
	TagName      string    `json:"tagName"`
	IsDraft      bool      `json:"isDraft"`
	IsPrerelease bool      `json:"isPrerelease"`
	PublishedAt  time.Time `json:"publishedAt"`
	URL          string    `json:"url"`
	Assets       []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Source is the view with what Verify proved of its signed files.
func (v GHReleaseView) Source(verified *Result, bundleSHA256 string) FeedSource {
	s := FeedSource{Tag: v.TagName, Prerelease: v.IsPrerelease, PublishedAt: v.PublishedAt, URL: v.URL,
		Verified: verified, BundleSHA256: bundleSHA256}
	for _, a := range v.Assets {
		s.Assets = append(s.Assets, FeedSourceAsset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return s
}

// FeedBuild is what BuildFeeds writes the feeds of one channel from.
type FeedBuild struct {
	Repo         string
	Channel      string
	DownloadBase string // "" is DefaultDownloadBase
	GeneratedAt  time.Time
	Keep         int // 0 is FeedKeep
	// Withdrawn are the pulled releases, by version ("0.3.158"), with the reason.
	Withdrawn map[string]string
	Releases  []FeedSource
}

// FeedSkip is a release a platform's feed leaves out, and why.
type FeedSkip struct {
	Platform, Tag, Reason string
}

// BuildFeeds writes one feed per platform any listed release has an archive for. Each
// lists, newest first, the channel's newest Keep releases that are not withdrawn and the
// withdrawn ones among them, each with FeedFileNames of its platform. A release missing
// a file it must have (its signed files, its archive) is left out of that platform's
// feed and reported in skips.
func BuildFeeds(b FeedBuild) (feeds map[string]*Feed, skips []FeedSkip, err error) {
	if b.Channel != FeedStable && b.Channel != FeedBeta {
		return nil, nil, fmt.Errorf("feed: unknown channel %q", b.Channel)
	}
	keep := b.Keep
	if keep <= 0 {
		keep = FeedKeep
	}
	rels := make([]FeedSource, 0, len(b.Releases))
	for _, r := range b.Releases {
		if r.Verified == nil || r.Verified.Manifest == nil {
			return nil, nil, fmt.Errorf("feed: %s is not verified", r.Tag)
		}
		if !ValidTag(r.Tag) {
			return nil, nil, fmt.Errorf("feed: %q is not a release tag", r.Tag)
		}
		if b.Channel == FeedStable && (r.Prerelease || IsPrerelease(r.Tag)) {
			continue
		}
		rels = append(rels, r)
	}
	sort.SliceStable(rels, func(i, j int) bool { return CompareVersions(rels[i].Tag, rels[j].Tag) > 0 })
	platforms := map[string]bool{}
	for _, r := range rels {
		for p := range r.Verified.Manifest.Assets {
			platforms[p] = true
		}
	}
	feeds = map[string]*Feed{}
	for p := range platforms {
		f := &Feed{Schema: FeedSchema, Repo: b.Repo, Channel: b.Channel, Platform: FeedPlatform(p),
			GeneratedAt: b.GeneratedAt.UTC(), Releases: []FeedRelease{}}
		kept := 0
		for _, r := range rels {
			if kept >= keep {
				break
			}
			fr, reason := feedRelease(b, r, p)
			if reason != "" {
				skips = append(skips, FeedSkip{Platform: FeedPlatform(p), Tag: r.Tag, Reason: reason})
				continue
			}
			if why, ok := b.Withdrawn[strings.TrimPrefix(r.Tag, "v")]; ok {
				if why == "" {
					why = "withdrawn"
				}
				fr.Withdrawn = &FeedWithdrawn{Reason: why}
			} else {
				kept++
			}
			f.Releases = append(f.Releases, fr)
		}
		feeds[f.Platform] = f
	}
	return feeds, skips, nil
}

// feedRelease is r's entry in platform's feed, or why it has none.
func feedRelease(b FeedBuild, r FeedSource, platform string) (FeedRelease, string) {
	m := r.Verified.Manifest
	names, ok := FeedFileNames(m, r.Tag, platform, r.Verified.Checksums)
	if !ok {
		return FeedRelease{}, "no archive for " + platform
	}
	listed := map[string]FeedSourceAsset{}
	for _, a := range r.Assets {
		listed[a.Name] = a
	}
	fr := FeedRelease{Version: strings.TrimPrefix(r.Tag, "v"), Tag: r.Tag, Prerelease: r.Prerelease || IsPrerelease(r.Tag),
		PublishedAt: r.PublishedAt.UTC(), URL: r.URL, ChecksumsSHA256: r.Verified.ChecksumsSHA256,
		ManifestSHA256: r.Verified.Checksums[ManifestFile]}
	if m.Migrations != nil {
		fr.MigrationsSetSHA256 = m.Migrations.SetSHA256
	}
	for i, n := range names {
		a, ok := listed[n]
		if !ok {
			if i < 4 { // the signed files and the archive: no install without them
				return FeedRelease{}, n + " is not attached"
			}
			continue
		}
		want := ReleaseDownloadURL(b.DownloadBase, b.Repo, r.Tag, n)
		if a.URL != want {
			return FeedRelease{}, fmt.Sprintf("%s is at %s, not %s", n, a.URL, want)
		}
		sum := r.Verified.Checksums[n]
		switch n {
		case ChecksumsFile:
			sum = r.Verified.ChecksumsSHA256
		case BundleFile:
			sum = r.BundleSHA256
		}
		fr.Assets = append(fr.Assets, FeedAsset{Name: n, URL: a.URL, Size: a.Size, SHA256: sum})
	}
	return fr, ""
}

// MarshalFeed is a feed file's bytes (compact JSON, a newline at the end).
func MarshalFeed(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// FeedIndex is a channel's index.json: its platforms' files and what each lists.
type FeedIndex struct {
	Schema      int                 `json:"schema"`
	Repo        string              `json:"repo"`
	Channel     string              `json:"channel"`
	GeneratedAt time.Time           `json:"generated_at"`
	Platforms   []FeedIndexPlatform `json:"platforms"`
}

// FeedIndexPlatform is one platform's feed file in an index.
type FeedIndexPlatform struct {
	Platform  string   `json:"platform"`
	File      string   `json:"file"`
	SHA256    string   `json:"sha256"`
	Versions  []string `json:"versions"`            // what it lists that may be installed, newest first
	Withdrawn []string `json:"withdrawn,omitempty"` // what it lists as pulled
}

// BuildFeedIndex is the index of feeds (by platform) and their files' bytes.
func BuildFeedIndex(repo, channel string, at time.Time, feeds map[string]*Feed, files map[string][]byte) *FeedIndex {
	ix := &FeedIndex{Schema: FeedSchema, Repo: repo, Channel: channel, GeneratedAt: at.UTC(), Platforms: []FeedIndexPlatform{}}
	for p, f := range feeds {
		e := FeedIndexPlatform{Platform: p, File: p + ".json", SHA256: sha256Hex(files[p]), Versions: []string{}}
		for _, r := range f.Releases {
			if r.Withdrawn != nil {
				e.Withdrawn = append(e.Withdrawn, r.Version)
			} else {
				e.Versions = append(e.Versions, r.Version)
			}
		}
		ix.Platforms = append(ix.Platforms, e)
	}
	sort.Slice(ix.Platforms, func(i, j int) bool { return ix.Platforms[i].Platform < ix.Platforms[j].Platform })
	return ix
}

// FeedInput is a feed file a machine read and what it asked for.
type FeedInput struct {
	Data   []byte // <channel>/<platform>.json
	Bundle []byte // its .sigstore.json
	// Identity is the pinned repository (its feed signer is Identity.FeedSAN).
	Identity Identity
	// TrustedMaterial is the Sigstore trust root; nil uses the embedded one.
	TrustedMaterial root.TrustedMaterial
	Channel         string
	Platform        string // "linux-amd64"
	// DownloadBase is where the feed's files must be ("" is DefaultDownloadBase).
	DownloadBase string
}

// VerifyFeedSignature checks a feed or index file's bundle: update-feed.yml on main of
// the pinned repository signed exactly data (file names it in errors). It returns the
// signed time (the Rekor entry's, never the file's own word).
func VerifyFeedSignature(tm root.TrustedMaterial, id Identity, file string, data, rawBundle []byte) (time.Time, error) {
	if tm == nil {
		var err error
		if tm, err = EmbeddedTrustedRoot(); err != nil {
			return time.Time{}, err
		}
	}
	if len(data) > MaxFeed {
		return time.Time{}, fail(ErrFeed, file, fmt.Sprintf("larger than %d bytes", MaxFeed), nil)
	}
	ids, err := id.feedIdentities()
	if err != nil {
		return time.Time{}, fail(ErrIdentity, file, "build expected identity", err)
	}
	signedAt, _, err := verifyBundle(tm, ids, file+FeedBundleSuffix, rawBundle, data)
	return signedAt, err
}

// VerifyFeed checks a feed's signature (VerifyFeedSignature) and then everything it
// says: its schema, repository, channel and platform are the ones asked for; each
// release is a valid tag listed once, on the channel, with its signed files; each file a
// plain name downloaded from exactly the release's own address. Nothing of a feed that
// fails is used.
func VerifyFeed(in FeedInput) (*Feed, time.Time, error) {
	file := FeedPath(in.Channel, in.Platform)
	signedAt, err := VerifyFeedSignature(in.TrustedMaterial, in.Identity, file, in.Data, in.Bundle)
	if err != nil {
		return nil, time.Time{}, err
	}
	f, err := ParseFeed(in.Data, in.Identity.Repo(), in.Channel, in.Platform, in.DownloadBase)
	if err != nil {
		return nil, time.Time{}, err
	}
	return f, signedAt, nil
}

// ParseFeed reads and checks a feed file's content (VerifyFeed without the signature:
// the builder's own check of what it wrote).
func ParseFeed(data []byte, repo, channel, platform, downloadBase string) (*Feed, error) {
	file := FeedPath(channel, platform)
	bad := func(format string, args ...any) error { return fail(ErrFeed, file, fmt.Sprintf(format, args...), nil) }
	if len(data) > MaxFeed {
		return nil, bad("larger than %d bytes", MaxFeed)
	}
	var f Feed
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return nil, fail(ErrFeed, file, "decode", err)
	}
	switch {
	case f.Schema != FeedSchema:
		return nil, bad("schema %d, want %d", f.Schema, FeedSchema)
	case !strings.EqualFold(f.Repo, repo):
		return nil, bad("the feed of %s, want %s", f.Repo, repo)
	case f.Channel != channel:
		return nil, bad("the %s channel's feed, want %s", f.Channel, channel)
	case f.Platform != platform:
		return nil, bad("the feed of %s, want %s", f.Platform, platform)
	case f.GeneratedAt.IsZero():
		return nil, bad("no generated_at")
	case len(f.Releases) > maxFeedReleases:
		return nil, bad("%d releases listed, at most %d", len(f.Releases), maxFeedReleases)
	}
	seen := map[string]bool{}
	for _, r := range f.Releases {
		if !ValidTag(r.Tag) || r.Version != strings.TrimPrefix(r.Tag, "v") {
			return nil, bad("release %q (%q) is not a release tag", r.Tag, r.Version)
		}
		if seen[r.Version] {
			return nil, bad("%s listed twice", r.Tag)
		}
		seen[r.Version] = true
		if channel == FeedStable && (r.Prerelease || IsPrerelease(r.Tag)) {
			return nil, bad("%s is a pre-release in the stable feed", r.Tag)
		}
		if !hexRe.MatchString(r.ChecksumsSHA256) || !hexRe.MatchString(r.ManifestSHA256) {
			return nil, bad("%s: a digest is not SHA-256", r.Tag)
		}
		if r.MigrationsSetSHA256 != "" && !hexRe.MatchString(r.MigrationsSetSHA256) {
			return nil, bad("%s: migrations_set_sha256 is not SHA-256", r.Tag)
		}
		if r.Withdrawn != nil && strings.TrimSpace(r.Withdrawn.Reason) == "" {
			return nil, bad("%s is withdrawn without a reason", r.Tag)
		}
		names := map[string]bool{}
		for _, a := range r.Assets {
			if !validFileName(a.Name) || names[a.Name] {
				return nil, bad("%s: file %q", r.Tag, a.Name)
			}
			names[a.Name] = true
			if a.URL != ReleaseDownloadURL(downloadBase, repo, r.Tag, a.Name) {
				return nil, bad("%s: %s is downloaded from %s, not the release's own address", r.Tag, a.Name, a.URL)
			}
			if (a.SHA256 != "" && !hexRe.MatchString(a.SHA256)) || a.Size < 0 {
				return nil, bad("%s: %s has a bad digest or size", r.Tag, a.Name)
			}
		}
		for _, n := range []string{ChecksumsFile, BundleFile, ManifestFile} {
			if !names[n] {
				return nil, bad("%s lists no %s", r.Tag, n)
			}
		}
		if a, _ := r.Asset(ChecksumsFile); a.SHA256 != r.ChecksumsSHA256 {
			return nil, bad("%s: checksums.txt's digest differs from checksums_sha256", r.Tag)
		}
		if a, _ := r.Asset(ManifestFile); a.SHA256 != r.ManifestSHA256 {
			return nil, bad("%s: release.json's digest differs from release_json_sha256", r.Tag)
		}
	}
	return &f, nil
}

// ParseFeedIndex reads and checks an index file's content.
func ParseFeedIndex(data []byte, repo, channel string) (*FeedIndex, error) {
	file := channel + "/" + FeedIndexFile
	var ix FeedIndex
	if len(data) > MaxFeed {
		return nil, fail(ErrFeed, file, "too large", nil)
	}
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, fail(ErrFeed, file, "decode", err)
	}
	if ix.Schema != FeedSchema || !strings.EqualFold(ix.Repo, repo) || ix.Channel != channel {
		return nil, fail(ErrFeed, file, fmt.Sprintf("schema %d of %s's %s channel, want %d of %s's %s", ix.Schema, ix.Repo, ix.Channel, FeedSchema, repo, channel), nil)
	}
	for _, p := range ix.Platforms {
		if p.File != p.Platform+".json" || !validFileName(p.File) || !hexRe.MatchString(p.SHA256) {
			return nil, fail(ErrFeed, file, "platform "+p.Platform, nil)
		}
	}
	return &ix, nil
}
