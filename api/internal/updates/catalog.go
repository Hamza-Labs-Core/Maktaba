package updates

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// CheckInterval is how often the home asks GitHub (design §4: every 6 h, was 24 h).
const CheckInterval = 6 * time.Hour

// StaleAfter: no successful check for this long raises a warning (a freeze attack or a
// broken network, story 35.3 AC10).
const StaleAfter = 14 * 24 * time.Hour

// Notification kinds (README R9.2) this package raises. The web app localises
// "notify.<kind>".
const (
	NoticeAvailable       = "update.available"
	NoticeScheduled       = "update.scheduled"
	NoticeVerifyFailed    = "update.verify_failed"
	NoticeReleaseHeld     = "update.release_held"
	NoticeReleaseWithdrew = "update.release_withdrawn"
	NoticeCheckStale      = "update.check_stale"
	NoticeReplay          = "update.replay"
	NoticeRunFailed       = "update.run_failed"
)

// smallFileLimit bounds each downloaded signed file.
const smallFileLimit = 4 << 20

// Discoverer polls GitHub Releases, keeps release_catalog and verifies each new entry's
// signed small files on sight (story 35.3 AC2).
type Discoverer struct {
	Store    *Store
	Verifier Verifier
	Notifier notify.Notifier
	Log      *slog.Logger
	HTTP     *http.Client
	// BaseURL is the GitHub API base (tests: an httptest server).
	BaseURL string
	// Repo is "owner/name" (MAKTABA_UPDATE_REPO, which also changes the expected
	// signing identity inside the verifier).
	Repo  string
	Token string
	// Policy is the effective policy (channel, auto_install).
	Policy func(ctx context.Context) (Policy, error)
	// Current is the running version.
	Current string
	Now     func() time.Time

	mu         sync.Mutex
	retryAfter time.Time
}

func (d *Discoverer) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Discoverer) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

func (d *Discoverer) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (d *Discoverer) notify(ctx context.Context, kind string, p notify.Payload) {
	if d.Notifier == nil {
		return
	}
	if err := d.Notifier.Notify(ctx, kind, p); err != nil {
		d.log().Warn("update notification failed", "event", "update_notify_error", "kind", kind, "err", err.Error())
	}
}

// ghRelease is the part of a GitHub release we read.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (g ghRelease) release() Release {
	r := Release{Version: NormalizeVersion(g.TagName), Tag: g.TagName, Channel: ChannelStable,
		Prerelease: g.Prerelease, ReleaseURL: g.HTMLURL, Notes: g.Body}
	if g.Prerelease || IsPrerelease(g.TagName) {
		r.Channel = ChannelBeta
	}
	if !g.PublishedAt.IsZero() {
		t := g.PublishedAt.UTC()
		r.PublishedAt = &t
	}
	for _, a := range g.Assets {
		r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return r
}

// ErrRateLimited: GitHub asked us to wait (Retry-After honoured, story 35.3 EC3).
var ErrRateLimited = errors.New("github rate limited")

func (d *Discoverer) get(ctx context.Context, url string, api bool, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if api {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if d.Token != "" {
			req.Header.Set("Authorization", "Bearer "+d.Token)
		}
	} else {
		req.Header.Set("Accept", "application/octet-stream")
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0") {
		wait := time.Minute
		if s := resp.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				wait = time.Duration(n) * time.Second
			}
		} else if s := resp.Header.Get("X-RateLimit-Reset"); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				if w := time.Until(time.Unix(n, 0)); w > 0 {
					wait = w
				}
			}
		}
		d.mu.Lock()
		d.retryAfter = d.now().Add(wait)
		d.mu.Unlock()
		return nil, resp.StatusCode, fmt.Errorf("%w for %s", ErrRateLimited, wait)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(b)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: larger than %d bytes", url, limit)
	}
	return b, resp.StatusCode, nil
}

func (d *Discoverer) base() string {
	b := strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	if b == "" {
		b = "https://api.github.com"
	}
	return b
}

// pinnedAssets are r's assets whose download is the pinned repository's own: GitHub's
// /<repo>/releases/download/<tag>/<name> on github.com (or the host of the GitHub this
// home asks, an Enterprise one or a test's), or its API's asset URL. Any other is dropped
// (logged): a release's listing names no file elsewhere, and a machine downloads only
// what the catalog keeps.
func (d *Discoverer) pinnedAssets(r Release) []Asset {
	apiHost := ""
	if u, err := url.Parse(d.base()); err == nil {
		apiHost = strings.ToLower(u.Host)
	}
	repo := strings.ToLower(d.repo())
	var out []Asset
	for _, a := range r.Assets {
		u, err := url.Parse(a.URL)
		ok := err == nil && (u.Scheme == "https" || (u.Scheme == "http" && strings.ToLower(u.Host) == apiHost))
		if ok {
			host, p := strings.ToLower(u.Host), strings.ToLower(u.EscapedPath())
			// no dot segments: the path is the repository's as written, not one that climbs out
			ok = path.Clean(p) == p
			ok = ok && (((host == "github.com" || host == apiHost) && strings.HasPrefix(p, "/"+repo+"/releases/download/")) ||
				(host == apiHost && strings.HasPrefix(p, "/repos/"+repo+"/releases/assets/")))
		}
		if !ok {
			d.log().Warn("updates: a release asset outside the pinned repository is ignored", "tag", r.Tag,
				"asset", a.Name, "url", a.URL, "repo", d.repo(), "event", "update_asset_foreign")
			continue
		}
		out = append(out, a)
	}
	return out
}

func (d *Discoverer) repo() string {
	if r := strings.TrimSpace(d.Repo); r != "" {
		return r
	}
	return "Hamza-Labs-Core/Maktaba"
}

// CheckResult is what one check did.
type CheckResult struct {
	Seen     int      `json:"seen"`
	New      []string `json:"new,omitempty"`
	Verified []string `json:"verified,omitempty"`
	Failed   []string `json:"failed,omitempty"`
}

// Check asks GitHub once: every release of the allowed channel newer than the watermark
// goes into the catalog; each new (or re-tagged) entry has its three small files
// downloaded and verified. A failure to reach GitHub keeps the catalog as it was.
func (d *Discoverer) Check(ctx context.Context) (CheckResult, error) {
	var res CheckResult
	d.mu.Lock()
	wait := d.retryAfter
	d.mu.Unlock()
	if d.now().Before(wait) {
		return res, fmt.Errorf("%w until %s", ErrRateLimited, wait.Format(time.RFC3339))
	}
	pol := DefaultPolicy()
	if d.Policy != nil {
		if p, err := d.Policy(ctx); err == nil {
			pol = p
		}
	}
	st := d.Store.LoadState(ctx)
	now := d.now().UTC()
	st.LastCheckAt = &now
	if st.Since == nil {
		st.Since = &now
	}
	body, _, err := d.get(ctx, d.base()+"/repos/"+d.repo()+"/releases?per_page=30", true, 8<<20)
	if err != nil {
		st.LastCheckErr = err.Error()
		d.Store.SaveState(ctx, st)
		d.staleHint(ctx, st)
		return res, err
	}
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		st.LastCheckErr = "decode releases: " + err.Error()
		d.Store.SaveState(ctx, st)
		return res, fmt.Errorf("decode releases: %w", err)
	}
	wm, err := d.Store.Watermark(ctx)
	if err != nil {
		return res, err
	}
	floor := maxVersion(NormalizeVersion(d.Current), wm)
	for _, g := range rels {
		if g.Draft || !ValidVersion(g.TagName) {
			continue
		}
		r := g.release()
		if pol.Channel != ChannelBeta && r.IsPrerelease() {
			continue
		}
		if floor != "" && CompareVersions(r.Version, floor) < 0 {
			continue
		}
		r.Assets = d.pinnedAssets(r)
		res.Seen++
		created, err := d.Store.Seen(ctx, r)
		if err != nil {
			return res, err
		}
		if created {
			res.New = append(res.New, r.Version)
		}
		cur, err := d.Store.Get(ctx, r.Version)
		if err != nil {
			return res, err
		}
		if cur.WithdrawnAt != nil {
			continue
		}
		if cur.VerifyState == VerifyVerified || cur.VerifyState == VerifyFailed {
			// a re-tag (story 35.3 EC4): a different checksums.txt at the same version
			d.retagCheck(ctx, cur, r, wm)
			continue
		}
		if cur.VerifyError != nil && cur.VerifyError.Kind == KindNoManifest && !created && !hasSignedFiles(r) {
			// still without its signed files: a release from before story 35.1, or one seen
			// before its workflow attached them (the release exists minutes earlier): it is
			// verified once they are there
			continue
		}
		switch ok, ve := d.verify(ctx, r); {
		case ok:
			res.Verified = append(res.Verified, r.Version)
			if CompareVersions(r.Version, NormalizeVersion(d.Current)) > 0 && !pol.EffectiveAutoInstall() {
				d.notify(ctx, NoticeAvailable, notify.Payload{Severity: notify.SeverityInfo,
					Params:  map[string]any{"version": r.Version},
					Link:    "/settings#updates",
					Subject: "release:" + r.Version, Every: notifyOnce})
			}
		case ve != nil && ve.Incident():
			res.Failed = append(res.Failed, r.Version)
		}
	}
	st.LastCheckOK = &now
	st.LastCheckErr = ""
	d.Store.SaveState(ctx, st)
	return res, nil
}

// staleHint raises the 14-day warning (once a day at most).
func (d *Discoverer) staleHint(ctx context.Context, st State) {
	last := time.Time{}
	switch {
	case st.LastCheckOK != nil:
		last = *st.LastCheckOK
	case st.Since != nil:
		last = *st.Since
	}
	if last.IsZero() || d.now().Sub(last) < StaleAfter {
		return
	}
	d.notify(ctx, NoticeCheckStale, notify.Payload{Severity: notify.SeverityWarn,
		Params:  map[string]any{"days": int(d.now().Sub(last).Hours() / 24), "error": st.LastCheckErr},
		Link:    "/settings#updates",
		Subject: "update_check", Every: 24 * time.Hour})
}

// hasSignedFiles reports whether a release (as GitHub lists it now) carries its three
// small signed files: checksums.txt, its Sigstore bundle and release.json.
func hasSignedFiles(r Release) bool {
	return assetURL(r, ChecksumsFile) != "" && assetURL(r, BundleFile) != "" && assetURL(r, ManifestFile) != ""
}

func assetURL(r Release, name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// download fetches a release's three small files; a missing release.json or bundle is
// KindNoManifest (a release from before story 35.1).
func (d *Discoverer) download(ctx context.Context, r Release) (ReleaseFiles, *VerifyError) {
	var f ReleaseFiles
	for _, x := range []struct {
		name string
		dst  *[]byte
	}{{ChecksumsFile, &f.Checksums}, {BundleFile, &f.Bundle}, {ManifestFile, &f.Manifest}} {
		u := assetURL(r, x.name)
		if u == "" {
			return f, &VerifyError{Kind: KindNoManifest, File: x.name, Detail: "not published with the release"}
		}
		b, _, err := d.get(ctx, u, false, smallFileLimit)
		if err != nil {
			return f, &VerifyError{Kind: KindUnavailable, File: x.name, Detail: err.Error()}
		}
		*x.dst = b
	}
	return f, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// verify downloads and verifies one entry and records the result. A bad release is a
// critical notification at once, whatever the policy (design §4).
func (d *Discoverer) verify(ctx context.Context, r Release) (bool, *VerifyError) {
	files, ve := d.download(ctx, r)
	sum := ""
	if files.Checksums != nil {
		sum = sha256Hex(files.Checksums)
	}
	if ve == nil {
		v, err := d.verifier().VerifyRelease(ctx, r.Tag, files)
		if err == nil {
			if NormalizeVersion(v.Manifest.Version) != r.Version {
				ve = &VerifyError{Kind: KindManifest, File: ManifestFile,
					Detail: fmt.Sprintf("release.json says %s for tag %s", v.Manifest.Version, r.Tag)}
			} else {
				if v.ChecksumsSHA256 == "" {
					v.ChecksumsSHA256 = sum
				}
				if err := d.Store.SetVerified(ctx, r.Version, v); err != nil {
					d.log().Warn("update catalog write failed", "event", "update_catalog_error", "err", err.Error())
					return false, nil
				}
				d.Store.Audit(ctx, "", "system", "release_verified", r.Version, map[string]any{
					"tag": r.Tag, "identity": v.Identity, "checksums_sha256": v.ChecksumsSHA256})
				return true, nil
			}
		} else {
			ve = AsVerifyError(err)
		}
	}
	if err := d.Store.SetVerifyError(ctx, r.Version, ve, sum); err != nil {
		d.log().Warn("update catalog write failed", "event", "update_catalog_error", "err", err.Error())
	}
	if ve.Incident() {
		d.log().Error("release verification failed", "event", "update_verify_failed", "version", r.Version,
			"kind", ve.Kind, "file", ve.File, "detail", ve.Detail)
		d.Store.Audit(ctx, "", "system", "release_verify_failed", r.Version, map[string]any{
			"tag": r.Tag, "kind": ve.Kind, "file": ve.File, "detail": ve.Detail})
		d.notify(ctx, NoticeVerifyFailed, notify.Payload{Severity: notify.SeverityError,
			Params:  map[string]any{"version": r.Version, "kind": ve.Kind, "file": ve.File, "detail": ve.Detail},
			Link:    "/settings#updates",
			Subject: "release:" + r.Version + ":" + sum, Every: notifyOnce})
	}
	return false, ve
}

func (d *Discoverer) verifier() Verifier {
	if d.Verifier != nil {
		return d.Verifier
	}
	return Unwired{}
}

// retagCheck compares the published checksums.txt with the recorded one: a different
// digest at an installed version is a replay (ErrReplay); otherwise the entry is
// re-verified as new and notified (story 35.3 EC4).
func (d *Discoverer) retagCheck(ctx context.Context, cur, fresh Release, watermark string) {
	if cur.ChecksumsSHA256 == "" {
		return
	}
	u := assetURL(fresh, ChecksumsFile)
	if u == "" {
		return
	}
	b, _, err := d.get(ctx, u, false, smallFileLimit)
	if err != nil {
		return
	}
	sum := sha256Hex(b)
	if sum == cur.ChecksumsSHA256 {
		return
	}
	installed := (watermark != "" && CompareVersions(cur.Version, watermark) <= 0) ||
		CompareVersions(cur.Version, d.Current) == 0
	params := map[string]any{"version": cur.Version, "was": cur.ChecksumsSHA256, "now": sum}
	if installed {
		ve := &VerifyError{Kind: KindReplay, File: ChecksumsFile,
			Detail: fmt.Sprintf("%s was installed with checksums %s, the release now has %s", cur.Version, cur.ChecksumsSHA256, sum)}
		noteStore(d.log(), "SetVerifyError", d.Store.SetVerifyError(ctx, cur.Version, ve, ""))
		d.Store.Audit(ctx, "", "system", "release_replay", cur.Version, params)
		d.notify(ctx, NoticeReplay, notify.Payload{Severity: notify.SeverityError, Params: params,
			Link: "/settings#updates", Subject: "release:" + cur.Version + ":" + sum, Every: notifyOnce})
		return
	}
	noteStore(d.log(), "ResetForReverify", d.Store.ResetForReverify(ctx, cur.Version))
	d.Store.Audit(ctx, "", "system", "release_retagged", cur.Version, params)
	_, _ = d.verify(ctx, fresh)
}

// Withdrawal reasons (story 35.3 AC6).
const (
	WithdrawnGone       = "withdrawn"         // the GitHub release no longer exists
	WithdrawnPrerelease = "turned_prerelease" // now a prerelease while the channel is stable
	WithdrawnChecksums  = "checksums_changed" // a different checksums.txt than the verified one
)

// Recheck re-reads the GitHub release just before a run starts (R4.2, story 35.3 AC6):
// gone, turned prerelease on stable, or a different checksums.txt marks it withdrawn
// (and notifies); it returns the reason ("" when the release is still good). An error
// reaching GitHub is returned as is: the run does not start on an unknown answer.
func (d *Discoverer) Recheck(ctx context.Context, r Release, channel string) (string, error) {
	body, code, err := d.get(ctx, d.base()+"/repos/"+d.repo()+"/releases/tags/"+r.Tag, true, 8<<20)
	reason := ""
	switch {
	case code == http.StatusNotFound:
		reason = WithdrawnGone
	case err != nil:
		return "", err
	default:
		var g ghRelease
		if err := json.Unmarshal(body, &g); err != nil {
			return "", fmt.Errorf("decode release: %w", err)
		}
		fresh := g.release()
		if g.Draft {
			reason = WithdrawnGone
		} else if channel != ChannelBeta && fresh.IsPrerelease() {
			reason = WithdrawnPrerelease
		} else if u := assetURL(fresh, ChecksumsFile); u == "" {
			reason = WithdrawnChecksums
		} else {
			b, _, err := d.get(ctx, u, false, smallFileLimit)
			if err != nil {
				return "", err
			}
			if r.ChecksumsSHA256 != "" && sha256Hex(b) != r.ChecksumsSHA256 {
				reason = WithdrawnChecksums
			}
		}
	}
	if reason == "" {
		return "", nil
	}
	if err := d.Store.SetWithdrawn(ctx, r.Version, reason); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return reason, err
	}
	d.Store.Audit(ctx, "", "system", "release_withdrawn", r.Version, map[string]any{"reason": reason})
	d.notify(ctx, NoticeReleaseWithdrew, notify.Payload{Severity: notify.SeverityWarn,
		Params:  map[string]any{"version": r.Version, "reason": reason},
		Link:    "/settings#updates",
		Subject: "release:" + r.Version, Every: notifyOnce})
	return reason, nil
}

// Run checks at once and then every interval until ctx ends.
func (d *Discoverer) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = CheckInterval
	}
	tick := func() {
		if _, err := d.Check(ctx); err != nil && ctx.Err() == nil {
			d.log().Warn("update check failed", "event", "update_check_error", "err", err.Error())
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
