// Package releasefeed writes, checks and watches the release feed
// (shared/go/releaseverify/feed.go) for update-feed.yml.
//
//	release-feed generate --releases views.json --signed DIR --out DIR --repo OWNER/NAME
//	                      [--withdrawn JSON] [--require vX.Y.Z] [--keep N]
//	release-feed verify   --dir DIR --repo OWNER/NAME
//	release-feed live     --url BASE --expect DIR --tag vX.Y.Z --repo OWNER/NAME [--wait 40m]
//
// generate reads the published releases (`gh release view --json
// tagName,isDraft,isPrerelease,publishedAt,url,assets`, one object each, in a JSON array)
// and each one's three signed files (DIR/<tag>/checksums.txt, checksums.txt.sigstore.json,
// release.json); it verifies each with releaseverify.Verify, as a home does, and writes
// DIR/<channel>/<platform>.json and DIR/<channel>/index.json for stable and beta. A
// draft, a release without signed files and one that does not verify are left out (the
// last fails the run when it is --require's). --require fails the run unless the tag is
// listed, installable, in every platform feed of its channels.
//
// verify checks every feed file and its .sigstore.json in DIR with the code a home runs
// (releaseverify.VerifyFeed): what update-feed.yml signed is what a home will accept.
//
// live fetches the feed from BASE (maktaba.tv) until every feed file of --expect is
// served, verifies as a home does and lists --tag, or --wait passes (then it fails).
package releasefeed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

var errUsage = errors.New("usage")

// Main runs the command; it returns the exit code.
func Main(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "release-feed: generate | verify | live")
		return 2
	}
	switch args[0] {
	case "generate":
		return generateMain(args[1:], stdout, stderr, now)
	case "verify":
		return verifyMain(args[1:], stdout, stderr)
	case "live":
		return liveMain(args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "release-feed: unknown command %q\n", args[0])
	return 2
}

// Generate is what generate does, without the flags.
type Generate struct {
	Views     []releaseverify.GHReleaseView
	SignedDir string
	OutDir    string
	Repo      string
	Withdrawn map[string]string
	Require   string
	Keep      int
	Now       time.Time
	// Trusted is the Sigstore trust root releases are verified against (nil: the
	// embedded one, as a home has; a test's fake root).
	Trusted root.TrustedMaterial
	Log     io.Writer
}

// ParseWithdrawn reads the pulled releases: a JSON object of version (or tag) to reason
// (the repository variable UPDATE_FEED_WITHDRAWN); empty is none.
func ParseWithdrawn(s string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("withdrawn releases: %w (want {\"0.3.158\": \"reason\"})", err)
	}
	for k, v := range m {
		k = strings.TrimPrefix(strings.TrimSpace(k), "v")
		if !releaseverify.ValidTag("v" + k) {
			return nil, fmt.Errorf("withdrawn releases: %q is not a version", k)
		}
		if strings.TrimSpace(v) == "" {
			v = "withdrawn"
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

// Run verifies the releases and writes the feeds.
func (g Generate) Run() error {
	logf := func(format string, a ...any) {
		if g.Log != nil {
			_, _ = fmt.Fprintf(g.Log, format+"\n", a...)
		}
	}
	id, err := releaseverify.NewIdentity(g.Repo)
	if err != nil {
		return err
	}
	var srcs []releaseverify.FeedSource
	required := false
	for _, v := range g.Views {
		if v.IsDraft || !releaseverify.ValidTag(v.TagName) {
			continue
		}
		dir := filepath.Join(g.SignedDir, v.TagName)
		read := func(n string) []byte {
			b, _ := os.ReadFile(filepath.Join(dir, n))
			return b
		}
		sums, bundle, man := read(releaseverify.ChecksumsFile), read(releaseverify.BundleFile), read(releaseverify.ManifestFile)
		if len(sums) == 0 || len(bundle) == 0 || len(man) == 0 {
			if v.TagName == g.Require {
				return fmt.Errorf("%s: its signed files are missing from %s", v.TagName, dir)
			}
			logf("%s: no signed files (a release before story 35.1): left out", v.TagName)
			continue
		}
		in := releaseverify.Input{Tag: v.TagName, Identity: id, Checksums: sums, Bundle: bundle, Manifest: man,
			TrustedMaterial: g.Trusted}
		res, err := releaseverify.Verify(in)
		if err != nil {
			if v.TagName == g.Require {
				return fmt.Errorf("%s does not verify: %w", v.TagName, err)
			}
			logf("::warning::%s does not verify and is left out of the feed: %v", v.TagName, err)
			continue
		}
		if v.TagName == g.Require {
			required = true
		}
		srcs = append(srcs, v.Source(res, sha256Hex(bundle)))
	}
	if g.Require != "" && !required {
		return fmt.Errorf("%s is not a published release", g.Require)
	}
	for _, ch := range []string{releaseverify.FeedStable, releaseverify.FeedBeta} {
		feeds, skips, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: id.Repo(), Channel: ch,
			GeneratedAt: g.Now, Keep: g.Keep, Withdrawn: g.Withdrawn, Releases: srcs})
		if err != nil {
			return err
		}
		for _, s := range skips {
			logf("%s/%s: %s left out: %s", ch, s.Platform, s.Tag, s.Reason)
		}
		if err := os.MkdirAll(filepath.Join(g.OutDir, ch), 0o755); err != nil {
			return err
		}
		files := map[string][]byte{}
		for p, f := range feeds {
			b, err := releaseverify.MarshalFeed(f)
			if err != nil {
				return err
			}
			// what is written must be what a home reads
			if _, err := releaseverify.ParseFeed(b, id.Repo(), ch, p, ""); err != nil {
				return err
			}
			files[p] = b
			if err := os.WriteFile(filepath.Join(g.OutDir, releaseverify.FeedPath(ch, p)), b, 0o644); err != nil {
				return err
			}
			logf("%s: %d releases, %d bytes", releaseverify.FeedPath(ch, p), len(f.Releases), len(b))
		}
		ix, err := releaseverify.MarshalFeed(releaseverify.BuildFeedIndex(id.Repo(), ch, g.Now, feeds, files))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(g.OutDir, ch, releaseverify.FeedIndexFile), ix, 0o644); err != nil {
			return err
		}
		if g.Require != "" && (ch == releaseverify.FeedBeta || !releaseverify.IsPrerelease(g.Require)) {
			if err := listsEverywhere(feeds, g.Require, srcs); err != nil {
				return fmt.Errorf("%s channel: %w", ch, err)
			}
		}
	}
	return nil
}

// listsEverywhere: every platform the tag's release.json has an archive for has a feed
// that lists the tag, not withdrawn.
func listsEverywhere(feeds map[string]*releaseverify.Feed, tag string, srcs []releaseverify.FeedSource) error {
	for _, s := range srcs {
		if s.Tag != tag {
			continue
		}
		for p := range s.Verified.Manifest.Assets {
			f := feeds[releaseverify.FeedPlatform(p)]
			if f == nil || !listsInstallable(f, tag) {
				return fmt.Errorf("the %s feed does not list %s", releaseverify.FeedPlatform(p), tag)
			}
		}
	}
	return nil
}

func listsInstallable(f *releaseverify.Feed, tag string) bool {
	for _, r := range f.Releases {
		if r.Tag == tag && r.Withdrawn == nil {
			return true
		}
	}
	return false
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func generateMain(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("release-feed generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	views := fs.String("releases", "", "a JSON array of `gh release view --json tagName,isDraft,isPrerelease,publishedAt,url,assets`")
	signed := fs.String("signed", "", "the releases' signed files, DIR/<tag>/{checksums.txt,checksums.txt.sigstore.json,release.json}")
	out := fs.String("out", "", "where to write <channel>/<platform>.json and <channel>/index.json")
	repo := fs.String("repo", "", "the repository (owner/name)")
	withdrawn := fs.String("withdrawn", "", `the pulled releases, {"0.3.158": "reason"}`)
	require := fs.String("require", "", "fail unless this release is listed for every platform it has")
	keep := fs.Int("keep", releaseverify.FeedKeep, "releases each feed lists that may be installed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *views == "" || *signed == "" || *out == "" || *repo == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "release-feed generate: %v: --releases FILE --signed DIR --out DIR --repo OWNER/NAME\n", errUsage)
		return 2
	}
	b, err := os.ReadFile(*views)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "release-feed:", err)
		return 1
	}
	var vs []releaseverify.GHReleaseView
	if err := json.Unmarshal(b, &vs); err != nil {
		_, _ = fmt.Fprintln(stderr, "release-feed: the releases:", err)
		return 1
	}
	w, err := ParseWithdrawn(*withdrawn)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "release-feed:", err)
		return 1
	}
	g := Generate{Views: vs, SignedDir: *signed, OutDir: *out, Repo: *repo, Withdrawn: w, Require: *require,
		Keep: *keep, Now: now, Log: stdout}
	if err := g.Run(); err != nil {
		_, _ = fmt.Fprintln(stderr, "release-feed:", err)
		return 1
	}
	return 0
}

// VerifyDir checks every feed and index file in dir (<channel>/*.json) with its bundle,
// as a home would; it returns the files it checked.
func VerifyDir(dir, repo string, trusted root.TrustedMaterial) ([]string, error) {
	id, err := releaseverify.NewIdentity(repo)
	if err != nil {
		return nil, err
	}
	var checked []string
	for _, ch := range []string{releaseverify.FeedStable, releaseverify.FeedBeta} {
		ents, err := os.ReadDir(filepath.Join(dir, ch))
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".json") || strings.HasSuffix(n, releaseverify.FeedBundleSuffix) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, ch, n))
			if err != nil {
				return nil, err
			}
			bundle, err := os.ReadFile(filepath.Join(dir, ch, n+releaseverify.FeedBundleSuffix))
			if err != nil {
				return nil, fmt.Errorf("%s/%s has no signature: %w", ch, n, err)
			}
			if err := verifyFile(id, trusted, ch, n, data, bundle); err != nil {
				return nil, err
			}
			checked = append(checked, ch+"/"+n)
		}
	}
	if len(checked) == 0 {
		return nil, fmt.Errorf("no feed files in %s", dir)
	}
	sort.Strings(checked)
	return checked, nil
}

func verifyFile(id releaseverify.Identity, tm root.TrustedMaterial, ch, name string, data, bundle []byte) error {
	if name == releaseverify.FeedIndexFile {
		if _, err := releaseverify.VerifyFeedSignature(tm, id, ch+"/"+name, data, bundle); err != nil {
			return err
		}
		_, err := releaseverify.ParseFeedIndex(data, id.Repo(), ch)
		return err
	}
	_, _, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: id,
		TrustedMaterial: tm, Channel: ch, Platform: strings.TrimSuffix(name, ".json")})
	return err
}

func verifyMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release-feed verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the signed feed: <channel>/<file>.json and <file>.json.sigstore.json")
	repo := fs.String("repo", "", "the repository (owner/name)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || *repo == "" {
		_, _ = fmt.Fprintf(stderr, "release-feed verify: %v: --dir DIR --repo OWNER/NAME\n", errUsage)
		return 2
	}
	files, err := VerifyDir(*dir, *repo, nil)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "release-feed: the signed feed a home would refuse:", err)
		return 1
	}
	for _, f := range files {
		_, _ = fmt.Fprintln(stdout, "verified", f)
	}
	return 0
}

// Live is the live check: the feed at URL serves, verified, every file of Expect's
// channels and platforms, each listing Tag where Expect does.
type Live struct {
	URL     string // https://maktaba.tv/updates/v1
	Expect  string // this run's signed feed folder
	Tag     string
	Repo    string
	HTTP    *http.Client
	Trusted root.TrustedMaterial // nil: the embedded root
}

// Check is one look at the live feed: nil when it serves what Expect has for Tag.
func (l Live) Check(ctx context.Context) error {
	id, err := releaseverify.NewIdentity(l.Repo)
	if err != nil {
		return err
	}
	c := l.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	var missing []string
	for _, ch := range []string{releaseverify.FeedStable, releaseverify.FeedBeta} {
		ents, err := os.ReadDir(filepath.Join(l.Expect, ch))
		if err != nil {
			return err
		}
		for _, e := range ents {
			n := e.Name()
			if !strings.HasSuffix(n, ".json") || strings.HasSuffix(n, releaseverify.FeedBundleSuffix) || n == releaseverify.FeedIndexFile {
				continue
			}
			want, err := os.ReadFile(filepath.Join(l.Expect, ch, n))
			if err != nil {
				return err
			}
			var wf releaseverify.Feed
			if err := json.Unmarshal(want, &wf); err != nil {
				return err
			}
			if !listsInstallable(&wf, l.Tag) {
				continue // this platform's feed does not carry the tag (a pre-release on stable)
			}
			data, err := get(ctx, c, l.URL+"/"+ch+"/"+n)
			if err == nil {
				var bundle []byte
				if bundle, err = get(ctx, c, l.URL+"/"+ch+"/"+n+releaseverify.FeedBundleSuffix); err == nil {
					var f *releaseverify.Feed
					f, _, err = releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: id,
						TrustedMaterial: l.Trusted, Channel: ch, Platform: strings.TrimSuffix(n, ".json")})
					if err == nil && !listsInstallable(f, l.Tag) {
						err = fmt.Errorf("does not list %s", l.Tag)
					}
				}
			}
			if err != nil {
				missing = append(missing, fmt.Sprintf("%s/%s: %v", ch, n, err))
			}
		}
	}
	if len(missing) > 0 {
		return errors.New(strings.Join(missing, "; "))
	}
	return nil
}

func get(ctx context.Context, c *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, releaseverify.MaxFeed+1))
	if err != nil {
		return nil, err
	}
	if len(b) > releaseverify.MaxFeed {
		return nil, fmt.Errorf("larger than %d bytes", releaseverify.MaxFeed)
	}
	return b, nil
}

func liveMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release-feed live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("url", releaseverify.DefaultFeedURL, "the live feed")
	expect := fs.String("expect", "", "this run's signed feed folder")
	tag := fs.String("tag", "", "the release every expected platform must list")
	repo := fs.String("repo", "", "the repository (owner/name)")
	wait := fs.Duration("wait", 40*time.Minute, "how long the live feed has to show it")
	every := fs.Duration("every", time.Minute, "how often to look")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *expect == "" || *tag == "" || *repo == "" {
		_, _ = fmt.Fprintf(stderr, "release-feed live: %v: --expect DIR --tag vX.Y.Z --repo OWNER/NAME [--url BASE]\n", errUsage)
		return 2
	}
	l := Live{URL: strings.TrimRight(*base, "/"), Expect: *expect, Tag: *tag, Repo: *repo}
	deadline := time.Now().Add(*wait)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := l.Check(ctx)
		cancel()
		if err == nil {
			_, _ = fmt.Fprintf(stdout, "the live feed at %s lists %s for every platform, signed and verified\n", l.URL, l.Tag)
			return 0
		}
		if time.Now().Add(*every).After(deadline) {
			_, _ = fmt.Fprintf(stderr, "::error title=release feed::the live feed at %s does not list %s for every platform after %s: %v\n", l.URL, l.Tag, *wait, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "not yet: %v\n", err)
		time.Sleep(*every)
	}
}
