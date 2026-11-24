package updates

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// FeedSource is the release feed (shared/go/releaseverify/feed.go) a Discoverer reads
// before GitHub: one small signed file per platform its machines run, listing only the
// releases they can install. GitHub's list is read instead only when the feed cannot be
// reached; a feed that is reached and fails its checks (its signature, its content, an
// older feed than one already read) stops the check, and nothing of it is used.
type FeedSource struct {
	// URL is the feed's base (releaseverify.DefaultFeedURL; a test's server).
	URL string
	// DownloadBase is where the feed's files must be ("" is GitHub; a test's server).
	DownloadBase string
	// Platforms are the release.json platforms ("linux/amd64") of the home and its
	// machines; nil is this machine's own.
	Platforms func(ctx context.Context) []string
	// Verifier checks a feed file with the pinned identity and trust root.
	Verifier FeedVerifier
}

// FeedVerifier verifies a feed file (ReleaseVerifier is one).
type FeedVerifier interface {
	VerifyFeed(ctx context.Context, in releaseverify.FeedInput) (*releaseverify.Feed, time.Time, error)
}

// VerifyFeed implements FeedVerifier with the identity and the trusted root the release
// verification uses.
func (v ReleaseVerifier) VerifyFeed(ctx context.Context, in releaseverify.FeedInput) (*releaseverify.Feed, time.Time, error) {
	id, err := v.Trust.Identity()
	if err != nil {
		return nil, time.Time{}, err
	}
	tm, err := v.Trust.TrustedMaterial(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	in.Identity, in.TrustedMaterial = id, tm
	return releaseverify.VerifyFeed(in)
}

// FeedURLEnv turns the feed off ("off": GitHub's list only). The feed's address is not
// a setting: it is releaseverify.DefaultFeedURL, and only for the pinned repository.
const FeedURLEnv = "MAKTABA_UPDATE_FEED"

// DefaultFeed is the feed of repo ("" is releaseverify.DefaultRepo): maktaba.tv's, for
// the default repository only (a fork's homes read their GitHub: maktaba.tv's feed is
// signed by the default repository and would never verify for them), and none when
// MAKTABA_UPDATE_FEED is "off".
func DefaultFeed(repo string, getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.EqualFold(strings.TrimSpace(getenv(FeedURLEnv)), "off") {
		return ""
	}
	if r := strings.TrimSpace(repo); r != "" && !strings.EqualFold(r, releaseverify.DefaultRepo) {
		return ""
	}
	return releaseverify.DefaultFeedURL
}

// errFeedUnreachable marks a feed that could not be read at all (the network, a
// status other than 200), and errFeedStale one signed before the one this home read last
// (maktaba.tv rolled back, or an old feed served again): the cases GitHub's list is read
// instead. Neither is a feed that failed its checks.
var (
	errFeedUnreachable = errors.New("the update feed could not be reached")
	errFeedStale       = errors.New("the update feed is older than the one read before")
)

func feedFallsBack(err error) bool {
	return errors.Is(err, errFeedUnreachable) || errors.Is(err, errFeedStale)
}

// feedChannel is the feed a policy's channel reads.
func feedChannel(channel string) string {
	if channel == ChannelBeta {
		return releaseverify.FeedBeta
	}
	return releaseverify.FeedStable
}

// feedPlatforms are the platforms whose feeds the home reads: its own first, then its
// machines' (each once).
func (d *Discoverer) feedPlatforms(ctx context.Context) []string {
	own := releaseverify.Platform()
	seen := map[string]bool{own: true}
	out := []string{own}
	if d.Feed.Platforms != nil {
		var others []string
		for _, p := range d.Feed.Platforms(ctx) {
			if p = strings.TrimSpace(p); p != "" && strings.Contains(p, "/") && !seen[p] {
				seen[p] = true
				others = append(others, p)
			}
		}
		sort.Strings(others)
		out = append(out, others...)
	}
	return out
}

// feedFile reads one feed file and its bundle. No answer, a 5xx or a 429 is
// errFeedUnreachable (code: the feed file's status, 0 when none came); any other status
// on the bundle once the feed file came is an unsigned feed.
func (d *Discoverer) feedFile(ctx context.Context, path string) (data, bundle []byte, code int, err error) {
	base := strings.TrimRight(d.Feed.URL, "/")
	data, code, err = d.get(ctx, base+"/"+path, false, releaseverify.MaxFeed)
	if err != nil {
		if strings.Contains(err.Error(), "larger than") {
			return nil, nil, code, fmt.Errorf("%s: %w", path, err)
		}
		return nil, nil, code, fmt.Errorf("%w: %s: %v", errFeedUnreachable, path, err)
	}
	bundle, bcode, err := d.get(ctx, base+"/"+path+releaseverify.FeedBundleSuffix, false, releaseverify.MaxFeed)
	if err != nil {
		if bcode == 0 || bcode >= 500 || bcode == http.StatusTooManyRequests {
			return nil, nil, code, fmt.Errorf("%w: %s%s: %v", errFeedUnreachable, path, releaseverify.FeedBundleSuffix, err)
		}
		return nil, nil, code, fmt.Errorf("%s is not signed: %w: %v", path, releaseverify.ErrSignature, err)
	}
	return data, bundle, code, nil
}

// feedList reads the channel's feed of the home's platform and of every platform its
// machines run. The home's own feed says which releases there are; the others add their
// platforms' files to those releases. withdrawn names the releases the feed pulled (tag →
// reason). A file signed before the one this home read last (st.FeedSignedAt), or more
// than releaseverify.FeedMaxAge ago, is errFeedStale: it would hide the releases and pulls
// made since, so GitHub's list is read instead (a release pulled since stays withdrawn in
// the catalog). A machine's platform whose feed cannot be read is passed over (logged):
// its files then come from beside checksums.txt (the catalog keeps every signed file).
func (d *Discoverer) feedList(ctx context.Context, channel string, st *State) (rels []ghRelease, withdrawn map[string]string, err error) {
	if d.Feed.Verifier == nil {
		return nil, nil, errors.New("the update feed has no verifier")
	}
	ch := feedChannel(channel)
	byTag := map[string]*ghRelease{}
	withdrawn = map[string]string{}
	signed := map[string]time.Time{}
	for i, p := range d.feedPlatforms(ctx) {
		own := i == 0
		fp := releaseverify.FeedPlatform(p)
		path := releaseverify.FeedPath(ch, fp)
		data, bundle, _, err := d.feedFile(ctx, path)
		if err != nil {
			if !own && errors.Is(err, errFeedUnreachable) {
				d.log().Warn("update feed: a machine's platform has no feed to read", "event", "update_feed_platform_missing",
					"platform", fp, "err", err.Error())
				continue
			}
			return nil, nil, err
		}
		f, at, err := d.Feed.Verifier.VerifyFeed(ctx, releaseverify.FeedInput{Data: data, Bundle: bundle,
			Channel: ch, Platform: fp, DownloadBase: d.Feed.DownloadBase})
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		if last, ok := st.FeedSignedAt[path]; ok && at.Before(last) {
			return nil, nil, fmt.Errorf("%w: %s signed %s, the one read before %s", errFeedStale, path,
				at.Format(time.RFC3339), last.Format(time.RFC3339))
		}
		if age := d.now().Sub(at); age > releaseverify.FeedMaxAge {
			return nil, nil, fmt.Errorf("%w: %s signed %s, more than %s ago", errFeedStale, path,
				at.Format(time.RFC3339), releaseverify.FeedMaxAge)
		}
		signed[path] = at
		for _, r := range f.Releases {
			g := byTag[r.Tag]
			if g == nil {
				if !own {
					continue // the home's own feed says which releases there are
				}
				if r.Withdrawn != nil {
					withdrawn[r.Tag] = r.Withdrawn.Reason
				}
				g = &ghRelease{TagName: r.Tag, HTMLURL: r.URL, Prerelease: r.Prerelease, PublishedAt: r.PublishedAt}
				byTag[r.Tag] = g
			}
			have := map[string]bool{}
			for _, a := range g.Assets {
				have[a.Name] = true
			}
			for _, a := range r.Assets {
				if !have[a.Name] {
					g.Assets = append(g.Assets, struct {
						Name string `json:"name"`
						URL  string `json:"browser_download_url"`
						Size int64  `json:"size"`
					}{a.Name, a.URL, a.Size})
				}
			}
		}
	}
	for _, g := range byTag {
		rels = append(rels, *g)
	}
	sort.Slice(rels, func(i, j int) bool { return CompareVersions(rels[i].TagName, rels[j].TagName) > 0 })
	if st.FeedSignedAt == nil {
		st.FeedSignedAt = map[string]time.Time{}
	}
	for k, v := range signed {
		st.FeedSignedAt[k] = v
	}
	return rels, withdrawn, nil
}

// signedAssets are r's files from the feed and, for every other file its signed
// checksums.txt lists (packs, first-party plugins, another platform's files), its address
// beside checksums.txt: a machine's update asks for files by name (MachineJob.Assets), and
// each is checked against checksums.txt when it is downloaded.
func signedAssets(r Release, checksums []byte) []Asset {
	sums, err := releaseverify.ParseChecksums(checksums)
	if err != nil {
		return r.Assets
	}
	have := map[string]bool{}
	for _, a := range r.Assets {
		have[a.Name] = true
	}
	out := append([]Asset(nil), r.Assets...)
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if have[n] {
			continue
		}
		if u := siblingURL(r, n); u != "" {
			out = append(out, Asset{Name: n, URL: u})
		}
	}
	return out
}

// keepSignedAssets: the files the catalog already holds for r (signedAssets of an earlier
// check) that the feed does not list are kept, so each check's feed does not drop them.
func keepSignedAssets(fresh Release, held []Asset) []Asset {
	have := map[string]bool{}
	for _, a := range fresh.Assets {
		have[a.Name] = true
	}
	out := append([]Asset(nil), fresh.Assets...)
	for _, a := range held {
		if !have[a.Name] && siblingURL(fresh, a.Name) == a.URL {
			out = append(out, a)
		}
	}
	return out
}

// listing is the releases to look at: the feed's when it has one, else GitHub's list
// (also when the feed cannot be reached or is older than the last). A feed refused is the
// check's error.
func (d *Discoverer) listing(ctx context.Context, channel string, st *State) (rels []ghRelease, withdrawn map[string]string, decodeErr, err error) {
	st.LastCheckSource = SourceGitHub
	st.FeedError = ""
	if d.Feed != nil && d.Feed.URL != "" {
		rels, withdrawn, err = d.feedList(ctx, channel, st)
		switch {
		case err == nil:
			st.LastCheckSource = SourceFeed
			return rels, withdrawn, nil, nil
		case !feedFallsBack(err):
			d.log().Error("update feed refused", "event", "update_feed_refused", "err", err.Error())
			st.LastCheckSource = SourceFeed
			return nil, nil, nil, fmt.Errorf("update feed refused: %w", err)
		}
		st.FeedError = err.Error()
		d.log().Warn("update feed not read: reading GitHub's release list", "event", "update_feed_fallback", "err", err.Error())
	}
	rels, decodeErr, err = d.list(ctx)
	return rels, nil, decodeErr, err
}

// Where a check found its releases (State.LastCheckSource).
const (
	SourceFeed   = "feed"
	SourceGitHub = "github"
)

// pull marks a release the feed withdrew (once) and says so, as Recheck does for one
// GitHub withdrew.
func (d *Discoverer) pull(ctx context.Context, version, why string) {
	if err := d.Store.SetWithdrawn(ctx, version, WithdrawnGone); err != nil {
		d.log().Warn("update catalog write failed", "event", "update_catalog_error", "err", err.Error())
		return
	}
	d.Store.Audit(ctx, "", "system", "release_withdrawn", version, map[string]any{"reason": WithdrawnGone,
		"source": SourceFeed, "detail": why})
	d.notify(ctx, NoticeReleaseWithdrew, notify.Payload{Severity: notify.SeverityWarn,
		Params:  map[string]any{"version": version, "reason": WithdrawnGone},
		Link:    "/admin/updates",
		Subject: "release:" + version, Every: notifyOnce})
}

// feedWithdrawn re-reads the feed before a run (Recheck): the reason the feed pulled
// r, or "" when it did not (or does not list it). ok is false when the feed could not
// be reached (Recheck then asks GitHub as before).
func (d *Discoverer) feedWithdrawn(ctx context.Context, r Release, channel string) (reason string, ok bool, err error) {
	if d.Feed == nil || d.Feed.URL == "" {
		return "", false, nil
	}
	st := d.Store.LoadState(ctx)
	_, withdrawn, err := d.feedList(ctx, channel, &st)
	switch {
	case feedFallsBack(err):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("update feed refused: %w", err)
	}
	d.Store.SaveState(ctx, st)
	return withdrawn[r.Tag], true, nil
}
