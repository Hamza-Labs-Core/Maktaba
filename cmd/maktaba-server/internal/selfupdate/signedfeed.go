package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// errFeedUnreachable: the feed could not be read at all; Latest reads GitHub's list.
var errFeedUnreachable = errors.New("the update feed could not be reached")

// feedLatest is the newest release this platform's feed lists that was not pulled.
func (c *Client) feedLatest(ctx context.Context) (*Release, error) {
	ch := releaseverify.FeedStable
	if c.o.Channel == "beta" {
		ch = releaseverify.FeedBeta
	}
	plat := c.o.Platform
	if plat == "" {
		plat = releaseverify.Platform()
	}
	path := releaseverify.FeedPath(ch, releaseverify.FeedPlatform(plat))
	data, code, err := c.feedGet(ctx, c.o.FeedURL+"/"+path)
	if err != nil {
		if code == http.StatusOK {
			return nil, fmt.Errorf("update feed refused: %s: %w", path, err)
		}
		return nil, fmt.Errorf("%w: %s: %v", errFeedUnreachable, path, err)
	}
	bundle, code, err := c.feedGet(ctx, c.o.FeedURL+"/"+path+releaseverify.FeedBundleSuffix)
	if err != nil {
		if code == 0 || code >= 500 {
			return nil, fmt.Errorf("%w: %s: %v", errFeedUnreachable, path, err)
		}
		return nil, fmt.Errorf("update feed refused: %s is not signed: %v", path, err)
	}
	tm, err := c.o.Trusted(ctx)
	if err != nil {
		return nil, err
	}
	f, signedAt, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: c.o.Identity,
		TrustedMaterial: tm, Channel: ch, Platform: releaseverify.FeedPlatform(plat), DownloadBase: c.o.FeedDownloadBase})
	if err != nil {
		return nil, fmt.Errorf("update feed refused: %w", err)
	}
	if time.Since(signedAt) > releaseverify.FeedMaxAge {
		// an old feed served again: GitHub's list says what there is
		return nil, fmt.Errorf("%w: %s was signed %s", errFeedUnreachable, path, signedAt.Format("2006-01-02"))
	}
	var best *releaseverify.FeedRelease
	for i := range f.Releases {
		r := &f.Releases[i]
		if r.Withdrawn != nil {
			continue
		}
		if best == nil || releaseverify.CompareVersions(r.Tag, best.Tag) > 0 {
			best = r
		}
	}
	if best == nil {
		return nil, ErrNoRelease
	}
	out := &Release{Tag: best.Tag, Prerelease: best.Prerelease, Assets: map[string]string{}}
	for _, a := range best.Assets {
		out.Assets[a.Name] = a.URL
	}
	return out, nil
}

func (c *Client) feedGet(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.o.HTTP.Do(req)
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
		return nil, http.StatusOK, fmt.Errorf("larger than %d bytes", releaseverify.MaxFeed)
	}
	return b, http.StatusOK, nil
}
