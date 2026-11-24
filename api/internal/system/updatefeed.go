package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// errFeedUnreachable: the release feed could not be read at all (the network, a status
// other than 200), the one case the check reads GitHub's list instead.
var errFeedUnreachable = errors.New("the update feed could not be reached")

// releases is what the check chooses from: this machine's platform's release feed
// (releaseverify.VerifyFeed: signed by update-feed.yml, the releases it may install,
// pulled ones left out) when there is one, else GitHub's list. GitHub's list is read
// only when the feed cannot be reached; a feed that fails its checks fails the check.
func (u *Updater) releases(ctx context.Context) ([]ghRelease, error) {
	if u.feedURL == "" || strings.EqualFold(strings.TrimSpace(os.Getenv("MAKTABA_UPDATE_FEED")), "off") {
		return u.fetchReleases(ctx)
	}
	rels, err := u.feedReleases(ctx)
	if err == nil {
		return rels, nil
	}
	if !errors.Is(err, errFeedUnreachable) {
		return nil, fmt.Errorf("update feed refused: %w", err)
	}
	u.log.Warn("update feed unreachable: reading GitHub's release list", "event", "update_feed_fallback", "err", err.Error())
	return u.fetchReleases(ctx)
}

func (u *Updater) feedReleases(ctx context.Context) ([]ghRelease, error) {
	ch := releaseverify.FeedStable
	if u.channel == "beta" {
		ch = releaseverify.FeedBeta
	}
	plat := releaseverify.FeedPlatform(releaseverify.Platform())
	path := releaseverify.FeedPath(ch, plat)
	data, code, err := u.feedGet(ctx, u.feedURL+"/"+path)
	if err != nil {
		if code == http.StatusOK { // too large
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return nil, fmt.Errorf("%w: %s: %v", errFeedUnreachable, path, err)
	}
	bundle, code, err := u.feedGet(ctx, u.feedURL+"/"+path+releaseverify.FeedBundleSuffix)
	if err != nil {
		if code == 0 || code >= 500 {
			return nil, fmt.Errorf("%w: %s%s: %v", errFeedUnreachable, path, releaseverify.FeedBundleSuffix, err)
		}
		return nil, fmt.Errorf("%s is not signed: %v", path, err)
	}
	id, err := u.Identity()
	if err != nil {
		return nil, err
	}
	trust := u.feedTrust
	if trust == nil {
		trust = u.trustedMaterial
	}
	tm, err := trust(ctx)
	if err != nil {
		return nil, err
	}
	f, _, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: id,
		TrustedMaterial: tm, Channel: ch, Platform: plat, DownloadBase: u.feedDownloadBase()})
	if err != nil {
		return nil, err
	}
	var out []ghRelease
	for _, r := range f.Releases {
		if r.Withdrawn != nil {
			continue
		}
		g := ghRelease{TagName: r.Tag, HTMLURL: r.URL, Prerelease: r.Prerelease}
		for _, a := range r.Assets {
			g.Assets = append(g.Assets, ghAsset{Name: a.Name, URL: a.URL, Size: a.Size})
		}
		out = append(out, g)
	}
	return out, nil
}

// feedDownloadBase is where the feed's files are: GitHub, or a test's server (the API
// base it was given serves the downloads too).
func (u *Updater) feedDownloadBase() string {
	if u.baseURL == "https://api.github.com" {
		return ""
	}
	return u.baseURL
}

// feedGet reads one feed file, held to releaseverify.MaxFeed; code is the answer's
// status (0 when none came).
func (u *Updater) feedGet(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := u.httpc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, releaseverify.MaxFeed+1))
	if err != nil {
		return nil, 0, err
	}
	if len(b) > releaseverify.MaxFeed {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: larger than %d bytes", url, releaseverify.MaxFeed)
	}
	return b, resp.StatusCode, nil
}
