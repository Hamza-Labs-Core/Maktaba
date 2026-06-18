// Update-check service (Epic 28, Story 28.2).
//
// A background poller that asks the GitHub Releases API whether a newer
// Maktaba release exists on the operator's channel, caches the answer
// with a TTL, and serves it at GET /api/system/updates. GitHub is the
// canonical package source (Epic 28 README): release.yml already
// publishes every artifact, a checksums.txt, and cosign signatures
// there, so there is no second feed to operate.
//
// The fetch/select/compare core is pure and injectable (the http client
// and clock are fields), so the dangerous-free part is unit-testable
// offline; the only side effect is one outbound GET per refresh.
package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultRepo is the canonical Maktaba repository; overridable per-fork
// via MAKTABA_UPDATE_REPO so a downstream fork tracks its own releases.
const defaultRepo = "Hamza-Labs-Core/Maktaba"

// DefaultCheckInterval is the auto-check cadence when none is configured.
const DefaultCheckInterval = 24 * time.Hour

// Asset is one downloadable file attached to a GitHub release, trimmed to
// the fields the UI and the self-updater (Story 28.3) need.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// UpdateStatus is the GET /api/system/updates response (Story 28.2).
type UpdateStatus struct {
	Available      bool      `json:"available"`
	Disabled       bool      `json:"disabled,omitempty"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version,omitempty"`
	Channel        string    `json:"channel"`
	ReleaseURL     string    `json:"release_url,omitempty"`
	ReleaseNotes   string    `json:"release_notes,omitempty"`
	Assets         []Asset   `json:"assets,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
}

// UpdaterConfig configures an Updater. Zero values are sane: empty Repo
// falls back to the canonical repo, zero Interval to the default cadence.
type UpdaterConfig struct {
	// Repo is "owner/repo"; empty ⇒ defaultRepo.
	Repo string
	// CurrentVersion is the running build's version string (from the
	// version package). Compared against the latest release.
	CurrentVersion string
	// Channel is "stable" (skip prereleases) or "beta" (include them).
	Channel string
	// Interval is the background poll cadence; <= 0 disables the loop.
	Interval time.Duration
	// Disabled is the kill switch: no network calls at all.
	Disabled bool
	// Token is an optional GitHub token to raise the rate limit.
	Token string
	// Logger is used for rate-limited error logging; nil ⇒ slog default.
	Logger *slog.Logger
	// HTTPClient / Now are injection points for tests.
	HTTPClient *http.Client
	Now        func() time.Time
	// APIBaseURL overrides the GitHub API base (default
	// "https://api.github.com") — a test seam pointing at httptest.
	APIBaseURL string
}

// Updater polls GitHub Releases and caches the result per channel.
type Updater struct {
	repo     string
	current  string
	channel  string
	ttl      time.Duration
	disabled bool
	token    string
	baseURL  string
	log      *slog.Logger
	httpc    *http.Client
	now      func() time.Time

	mu      sync.RWMutex
	cache   map[string]UpdateStatus // keyed by channel
	lastErr time.Time               // for rate-limited error logging
}

// NewUpdater builds an Updater from cfg, applying defaults.
func NewUpdater(cfg UpdaterConfig) *Updater {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		repo = defaultRepo
	}
	channel := strings.TrimSpace(strings.ToLower(cfg.Channel))
	if channel == "" {
		channel = "stable"
	}
	ttl := cfg.Interval
	if ttl == 0 {
		ttl = DefaultCheckInterval
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	httpc := cfg.HTTPClient
	if httpc == nil {
		httpc = &http.Client{Timeout: 15 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Updater{
		repo:     repo,
		current:  cfg.CurrentVersion,
		channel:  channel,
		ttl:      ttl,
		disabled: cfg.Disabled,
		token:    cfg.Token,
		baseURL:  baseURL,
		log:      log,
		httpc:    httpc,
		now:      now,
		cache:    make(map[string]UpdateStatus),
	}
}

// Status returns the current update status, using the cache unless
// refresh is true or the cached entry is older than the TTL. A fetch
// error never propagates: a stale cache (if any) is served, else an
// "unavailable" zero-result, so the endpoint never 500s on a transient
// GitHub outage.
func (u *Updater) Status(ctx context.Context, refresh bool) UpdateStatus {
	if u.disabled {
		return UpdateStatus{
			Disabled:       true,
			CurrentVersion: u.current,
			Channel:        u.channel,
			CheckedAt:      u.now(),
		}
	}
	if !refresh {
		if s, ok := u.fromCache(); ok {
			return s
		}
	}
	s, err := u.check(ctx)
	if err != nil {
		u.logErr(err)
		if s, ok := u.cachedAny(); ok {
			return s // serve stale on error
		}
		return UpdateStatus{CurrentVersion: u.current, Channel: u.channel, CheckedAt: u.now()}
	}
	u.store(s)
	return s
}

// Run primes the cache once and then refreshes on the TTL ticker until
// ctx is cancelled. A disabled updater or non-positive TTL is a no-op.
func (u *Updater) Run(ctx context.Context) {
	if u.disabled || u.ttl <= 0 {
		return
	}
	_ = u.Status(ctx, true)
	t := time.NewTicker(u.ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = u.Status(ctx, true)
		}
	}
}

// Handler serves GET /api/system/updates. ?refresh=true bypasses cache.
func (u *Updater) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refresh := r.URL.Query().Get("refresh") == "true"
		s := u.Status(r.Context(), refresh)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(s)
	})
}

func (u *Updater) fromCache() (UpdateStatus, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	s, ok := u.cache[u.channel]
	if !ok {
		return UpdateStatus{}, false
	}
	if u.now().Sub(s.CheckedAt) >= u.ttl {
		return UpdateStatus{}, false // stale
	}
	return s, true
}

// cachedAny returns the last cached entry for the active channel
// regardless of TTL — used to serve stale data when a refresh fails.
func (u *Updater) cachedAny() (UpdateStatus, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	s, ok := u.cache[u.channel]
	return s, ok
}

func (u *Updater) store(s UpdateStatus) {
	u.mu.Lock()
	u.cache[u.channel] = s
	u.mu.Unlock()
}

func (u *Updater) logErr(err error) {
	// Rate-limit error logging to once per minute so a long outage
	// doesn't flood the log.
	u.mu.Lock()
	last := u.lastErr
	now := u.now()
	if now.Sub(last) < time.Minute {
		u.mu.Unlock()
		return
	}
	u.lastErr = now
	u.mu.Unlock()
	u.log.Warn("update check failed", "event", "update_check_error", "err", err.Error())
}

// ghRelease is the subset of the GitHub Releases API we read.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// check fetches releases and computes the status against the current
// version on the active channel.
func (u *Updater) check(ctx context.Context) (UpdateStatus, error) {
	rels, err := u.fetchReleases(ctx)
	if err != nil {
		return UpdateStatus{}, err
	}
	status := UpdateStatus{
		CurrentVersion: u.current,
		Channel:        u.channel,
		CheckedAt:      u.now(),
	}
	latest, ok := selectLatest(rels, u.channel)
	if !ok {
		return status, nil // nothing applicable; not an error
	}
	status.LatestVersion = latest.TagName
	status.ReleaseURL = latest.HTMLURL
	status.ReleaseNotes = latest.Body
	status.Assets = toAssets(latest.Assets)
	if compareSemver(latest.TagName, u.current) > 0 {
		status.Available = true
	}
	return status, nil
}

func (u *Updater) fetchReleases(ctx context.Context) ([]ghRelease, error) {
	url := u.baseURL + "/repos/" + u.repo + "/releases?per_page=30"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
	}
	resp, err := u.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: HTTP %d", resp.StatusCode)
	}
	var rels []ghRelease
	// Cap the body so a hostile/buggy response can't OOM us.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rels); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return rels, nil
}

func toAssets(in []ghAsset) []Asset {
	out := make([]Asset, 0, len(in))
	for _, a := range in {
		out = append(out, Asset(a))
	}
	return out
}

// selectLatest returns the highest-versioned release allowed by channel.
// stable skips drafts, prereleases, and any tag with a prerelease suffix;
// beta includes prereleases. Unparseable tags are ignored.
func selectLatest(rels []ghRelease, channel string) (ghRelease, bool) {
	var best ghRelease
	found := false
	for _, r := range rels {
		if r.Draft {
			continue
		}
		pre := r.Prerelease || hasPrereleaseSuffix(r.TagName)
		if channel != "beta" && pre {
			continue
		}
		if !validSemver(r.TagName) {
			continue
		}
		if !found || compareSemver(r.TagName, best.TagName) > 0 {
			best = r
			found = true
		}
	}
	return best, found
}

func hasPrereleaseSuffix(tag string) bool {
	t := strings.ToLower(tag)
	return strings.Contains(t, "-beta") ||
		strings.Contains(t, "-rc") ||
		strings.Contains(t, "-alpha") ||
		strings.Contains(t, "nightly")
}

// --- semver: numeric per-segment, prerelease ranks below its release ---

// validSemver reports whether tag has at least one parseable numeric
// segment after stripping a leading "v" and any prerelease/build suffix.
func validSemver(tag string) bool {
	core, _ := splitSemver(tag)
	return len(core) > 0
}

// splitSemver returns the numeric core segments and whether a prerelease
// suffix is present.
func splitSemver(tag string) ([]int, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(tag)), "v")
	pre := false
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i] == '-'
		v = v[:i]
	}
	if v == "" {
		return nil, pre
	}
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, pre // non-numeric core ⇒ unparseable
		}
		out = append(out, n)
	}
	return out, pre
}

// compareSemver returns >0 if a is newer than b, <0 if older, 0 if equal.
// Numeric segments compare element-wise; a prerelease of the same core
// (1.5.0-rc.1) ranks below the release (1.5.0). Unparseable cores compare
// as 0-length (so "dev" is never newer than a real release).
func compareSemver(a, b string) int {
	ac, apre := splitSemver(a)
	bc, bpre := splitSemver(b)
	for i := 0; i < len(ac) || i < len(bc); i++ {
		var x, y int
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	// Equal numeric core: the release outranks its prerelease.
	switch {
	case apre && !bpre:
		return -1
	case !apre && bpre:
		return 1
	default:
		return 0
	}
}

// PlatformToken is the "<os>-<arch>" fragment release-archive names embed
// (e.g. "maktaba-server-1.4.2-linux-amd64.tar.gz"). Exported so Story
// 28.3's self-updater matches the asset for this host with one shared
// convention.
func PlatformToken() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}
