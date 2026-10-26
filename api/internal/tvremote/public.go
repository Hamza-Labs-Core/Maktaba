package tvremote

// Epic 34 (the public TV site), the home's part, first slice: the channels
// the owner made public (story 34.1, with the admin's rights confirmation),
// their push held always-on through Epic 32's push and relay (34.3), and the
// site state the publisher sends to the Worker (34.2, 34.4): the lineup and,
// per channel on air, the live video the Worker signs viewer tokens for.
//
// The Worker never calls the home: the home posts its state outward, signed
// with an HMAC secret both sides hold (design §6.6). A visitor only ever
// reaches the Worker and Cloudflare Stream.
//
// Not in this slice (specs/epics/34-tv-public-site): exclusion slates, the
// dedicated A/B public inputs (a public channel uses the Epic 32 pool, so it
// counts against tv_remote.max_channels and shares its rotation), the guide
// blobs in R2, LiveHub, access modes, branding, the deployer.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

const (
	// PublicSettingsKey holds the site's settings in app_settings.
	PublicSettingsKey = "tv_public"
	// SecretSiteSigningKey is the site's own Stream signing key ({id, pem}),
	// apart from the app's (cf_signing_key), so either can be rotated alone.
	SecretSiteSigningKey = "tv_public_signing_key"
	// SecretSiteHMAC signs the home's posts to the Worker.
	SecretSiteHMAC = "tv_public_home_hmac"
	// HoldPublic: the push is held by the public site (always on).
	HoldPublic = "public"
	// SiteStatePath is the Worker's endpoint for the home's state.
	SiteStatePath = "/_home/state"
	// PublishEvery is how often the home posts its state; the Worker takes
	// the site off-air when it hears nothing for 60 s (34.4 AC6).
	PublishEvery = 10 * time.Second
)

// PublicSettings are the site's settings (Admin → Live TV → Public site).
type PublicSettings struct {
	// Enabled: public channels are pushed and published.
	Enabled bool `json:"enabled"`
	// SiteURL is the Worker's address (its workers.dev host until a domain
	// is set: tv_public.domain, story 34.8).
	SiteURL string `json:"site_url"`
}

// LoadPublicSettings reads tv_public (zero when unset).
func LoadPublicSettings(ctx context.Context, db *sql.DB) PublicSettings {
	var ps PublicSettings
	if db == nil {
		return ps
	}
	var raw ns
	if db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, PublicSettingsKey).Scan(&raw) == nil {
		_ = json.Unmarshal([]byte(raw.S), &ps)
	}
	return ps
}

// ValidSiteURL accepts an https URL with no path, query or credentials, or
// plain http on the loopback (wrangler dev).
func ValidSiteURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return errors.New("site_url: the site's address, like https://maktaba-tv.example.workers.dev")
	}
	host := u.Hostname()
	if u.Scheme == "https" || (u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return nil
	}
	return errors.New("site_url: use https (http only for a local wrangler dev)")
}

// SavePublicSettings stores the site's settings.
func (c *Controller) SavePublicSettings(ctx context.Context, actor string, ps PublicSettings) error {
	c.init()
	ps.SiteURL = strings.TrimRight(strings.TrimSpace(ps.SiteURL), "/")
	if err := ValidSiteURL(ps.SiteURL); err != nil {
		return refuse(http.StatusBadRequest, "invalid_site_url", err.Error())
	}
	blob, _ := json.Marshal(ps)
	if _, err := c.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		PublicSettingsKey, string(blob), c.st.t(c.now())); err != nil {
		return err
	}
	c.audit(ctx, "tv_public.settings", actor, PublicSettingsKey, map[string]any{"enabled": ps.Enabled, "site_url": ps.SiteURL})
	c.Wake()
	return nil
}

// PublicChannel is a channel and its public-site row (none: never public).
type PublicChannel struct {
	ChannelID         string     `json:"channel_id"`
	Number            int        `json:"number"`
	Name              string     `json:"name"`
	Enabled           bool       `json:"enabled"`
	Public            bool       `json:"public"`
	Slug              string     `json:"slug"`
	RightsConfirmedBy *string    `json:"rights_confirmed_by"`
	RightsConfirmedAt *time.Time `json:"rights_confirmed_at"`
	RightsNote        string     `json:"rights_note"`
	// OnAir: a live push the site can play now.
	OnAir bool `json:"on_air"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// SlugOf makes a site slug from a channel's slug or name.
func SlugOf(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// PublicChannels lists every channel with its public-site state.
func (c *Controller) PublicChannels(ctx context.Context) ([]PublicChannel, error) {
	c.init()
	rows, err := c.DB.QueryContext(ctx, `
		SELECT c.id, c.number, c.name, c.slug, c.enabled, p.public, p.slug,
		       p.rights_confirmed_by, p.rights_confirmed_at, COALESCE(p.rights_note, '')
		FROM channels c LEFT JOIN tv_public_channels p ON p.channel_id = c.id
		ORDER BY c.number, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicChannel{}
	for rows.Next() {
		var x PublicChannel
		var id, chSlug, slug, by ns
		var enabled, public any
		var at nt
		if err := rows.Scan(&id, &x.Number, &x.Name, &chSlug, &enabled, &public, &slug, &by, &at, &x.RightsNote); err != nil {
			return nil, err
		}
		x.ChannelID, x.Enabled, x.Public, x.Slug = id.S, enabled == nil || boolOf(enabled), boolOf(public), slug.S
		if x.Slug == "" {
			x.Slug = SlugOf(chSlug.S)
		}
		if by.S != "" {
			v := by.S
			x.RightsConfirmedBy = &v
		}
		x.RightsConfirmedAt = at.ptr()
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	live := c.livePushes(ctx)
	for i := range out {
		_, out[i].OnAir = live[out[i].ChannelID]
	}
	return out, nil
}

// livePushes maps channel → its live push's video id.
func (c *Controller) livePushes(ctx context.Context) map[string]string {
	m := map[string]string{}
	rows, err := c.DB.QueryContext(ctx, `SELECT channel_id, video_uid FROM tv_remote_pushes WHERE state = 'live' AND video_uid IS NOT NULL`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var ch, v ns
		if rows.Scan(&ch, &v) == nil && v.S != "" {
			m[ch.S] = v.S
		}
	}
	return m
}

// PublicChange is an admin's edit of one channel's public state.
type PublicChange struct {
	Public *bool   `json:"public"`
	Slug   *string `json:"slug"`
	Note   *string `json:"rights_note"`
	// RightsConfirmed: the admin confirms the rights to show this channel
	// publicly; required each time a channel is made public (34.1 AC1, AC2).
	RightsConfirmed bool `json:"rights_confirmed"`
}

// SetPublic applies an admin's change to a channel's public state.
func (c *Controller) SetPublic(ctx context.Context, actor, channelID string, ch PublicChange) (PublicChannel, error) {
	c.init()
	all, err := c.PublicChannels(ctx)
	if err != nil {
		return PublicChannel{}, err
	}
	var cur *PublicChannel
	for i := range all {
		if all[i].ChannelID == channelID {
			cur = &all[i]
		}
	}
	if cur == nil {
		return PublicChannel{}, refuse(http.StatusNotFound, "no_such_channel", "no such channel")
	}
	next := *cur
	if ch.Slug != nil {
		next.Slug = strings.TrimSpace(*ch.Slug)
	}
	if !slugRe.MatchString(next.Slug) {
		return PublicChannel{}, refuse(http.StatusBadRequest, "invalid_slug",
			"slug: 1-40 lower-case letters, digits and dashes, starting with a letter or digit")
	}
	for _, o := range all {
		if o.ChannelID != channelID && o.Slug == next.Slug && (o.Public || o.RightsConfirmedAt != nil) {
			return PublicChannel{}, refuse(http.StatusConflict, "slug_taken", "another channel has the slug "+next.Slug)
		}
	}
	if ch.Note != nil {
		next.RightsNote = strings.TrimSpace(*ch.Note)
	}
	now := c.now()
	goingPublic := ch.Public != nil && *ch.Public && !cur.Public
	if goingPublic {
		if !ch.RightsConfirmed {
			return PublicChannel{}, refuse(http.StatusUnprocessableEntity, "rights_confirmation_required",
				"confirm you have the rights to show this channel to the public")
		}
		by := actor
		next.RightsConfirmedBy, next.RightsConfirmedAt = &by, &now
	}
	if ch.Public != nil {
		next.Public = *ch.Public
	}
	if _, err := c.DB.ExecContext(ctx, `
		INSERT INTO tv_public_channels (channel_id, public, slug, rights_confirmed_by, rights_confirmed_at, rights_note, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (channel_id) DO UPDATE SET public = EXCLUDED.public, slug = EXCLUDED.slug,
		    rights_confirmed_by = EXCLUDED.rights_confirmed_by, rights_confirmed_at = EXCLUDED.rights_confirmed_at,
		    rights_note = EXCLUDED.rights_note, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		channelID, c.boolParam(next.Public), next.Slug, nullable(deref(next.RightsConfirmedBy)), c.st.tp(next.RightsConfirmedAt),
		next.RightsNote, c.st.t(now), nullable(actor)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return PublicChannel{}, refuse(http.StatusConflict, "slug_taken", "another channel has the slug "+next.Slug)
		}
		return PublicChannel{}, err
	}
	switch {
	case goingPublic:
		c.audit(ctx, "tv_public.public", actor, channelID, map[string]any{"slug": next.Slug, "rights_note": next.RightsNote})
	case cur.Public && !next.Public:
		c.audit(ctx, "tv_public.private", actor, channelID, map[string]any{"slug": next.Slug})
	}
	c.log().Info("tv_public: channel updated", "channel_id", channelID, "public", next.Public, "slug", next.Slug,
		"by", actor, "event", "tv_public_channel")
	c.Wake()
	return next, nil
}

func (c *Controller) boolParam(b bool) any {
	if c.st.postgres {
		return b
	}
	if b {
		return 1
	}
	return 0
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// publicChannelIDs are the public channels that are switched on.
func (c *Controller) publicChannelIDs(ctx context.Context) []string {
	all, err := c.PublicChannels(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, x := range all {
		if x.Public && x.Enabled {
			out = append(out, x.ChannelID)
		}
	}
	return out
}

// publicTick holds every public channel's push and requests the ones that
// are missing: always on, with or without viewers (34.3 AC2).
func (c *Controller) publicTick(ctx context.Context, s Settings, now time.Time) {
	holds := map[string]bool{}
	defer func() {
		c.mu.Lock()
		c.pubHolds = holds
		c.mu.Unlock()
	}()
	if !LoadPublicSettings(ctx, c.DB).Enabled {
		return
	}
	for _, ch := range c.publicChannelIDs(ctx) {
		holds[ch] = true
	}
	if len(holds) == 0 {
		return
	}
	// the budget's block_new holds here as for a tune (32.10): no new push
	// once it is used up, though a running one keeps going
	exhausted := false
	if e, err := c.Estimate(ctx, s, now); err == nil && e.Exhausted {
		exhausted = true
	}
	if ready, why := c.SimulcastReady(ctx); !ready {
		c.noteOnce("public|"+why, func() {
			c.log().Warn("tv_public: public channels wait for the Cloudflare integration", "why", why, "event", "tv_public_waiting")
		})
		return
	}
	for ch := range holds {
		if _, running, err := c.st.runningPushFor(ctx, ch); err != nil || running || c.hasEndingPush(ctx, ch) {
			continue
		}
		if exhausted {
			c.noteOnce(ch+"|public|budget", func() {
				c.log().Warn("tv_public: the monthly budget is used up; a public channel waits", "channel_id", ch,
					"event", "tv_public_waiting")
			})
			continue
		}
		if _, err := c.lockedNewPush(ctx, s, ch, now); err != nil {
			why := err.Error()
			var te *TuneError
			if errors.As(err, &te) {
				why = te.Detail
			}
			c.noteOnce(ch+"|public|"+why, func() {
				c.log().Warn("tv_public: a public channel cannot start its push", "channel_id", ch, "why", why,
					"event", "tv_public_waiting")
			})
			continue
		}
		c.log().Info("tv_public: push requested for a public channel", "channel_id", ch, "event", "tv_public_push")
	}
}

func (c *Controller) publicHold(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pubHolds[channel]
}

// SiteChannel is one channel of the state posted to the Worker. VideoUID is
// for the Worker only (it signs viewer tokens for it); the site's public
// lineup leaves it out.
type SiteChannel struct {
	Slug     string `json:"slug"`
	Number   int    `json:"number"`
	Name     string `json:"name"`
	OnAir    bool   `json:"on_air"`
	VideoUID string `json:"video_uid,omitempty"`
}

// SiteState is the home's post to the Worker.
type SiteState struct {
	Schema       int           `json:"schema"`
	At           time.Time     `json:"at"`
	CustomerCode string        `json:"customer_code"`
	Channels     []SiteChannel `json:"channels"`
}

// SiteState is what the public site may show now: the public channels that
// are switched on, and the live video of each one on air.
func (c *Controller) SiteState(ctx context.Context) (SiteState, error) {
	c.init()
	st := SiteState{Schema: 1, At: c.now(), Channels: []SiteChannel{}}
	if c.Integration != nil {
		if cfg, err := c.Integration.load(ctx); err == nil {
			st.CustomerCode = cfg.code
		}
	}
	all, err := c.PublicChannels(ctx)
	if err != nil {
		return st, err
	}
	live := c.livePushes(ctx)
	for _, x := range all {
		if !x.Public || !x.Enabled {
			continue
		}
		v := live[x.ChannelID]
		st.Channels = append(st.Channels, SiteChannel{Slug: x.Slug, Number: x.Number, Name: x.Name, OnAir: v != "", VideoUID: v})
	}
	return st, nil
}

// SiteSecrets are given once, to paste into the Worker's secrets
// (wrangler secret put HOME_HMAC, STREAM_KEY_ID, STREAM_KEY_JWK): the home's
// HMAC secret and the site's signing key as a JWK (WebCrypto's form).
type SiteSecrets struct {
	HomeHMAC      string `json:"home_hmac"`
	SigningKeyID  string `json:"signing_key_id"`
	SigningKeyJWK string `json:"signing_key_jwk"`
}

// NewSiteSecrets creates the site's Stream signing key (deleting the
// previous one at Cloudflare) and a new HMAC secret. Every token the site
// minted with the old key stops working.
func (c *Controller) NewSiteSecrets(ctx context.Context, actor string) (SiteSecrets, error) {
	c.init()
	if c.Integration == nil || c.Integration.Secrets == nil {
		return SiteSecrets{}, errors.New("no secret store")
	}
	cl, _, err := c.client(ctx)
	if err != nil {
		return SiteSecrets{}, refuse(http.StatusServiceUnavailable, "remote_unavailable", err.Error())
	}
	k, err := cl.CreateSigningKey(ctx)
	if err != nil {
		return SiteSecrets{}, refuse(http.StatusBadGateway, "cloudflare_error", "create the site's signing key: "+err.Error())
	}
	if old, err := c.Integration.siteKey(ctx); err == nil && old.ID != "" && old.ID != k.ID {
		if err := cl.DeleteSigningKey(ctx, old.ID); err != nil {
			c.log().Warn("tv_public: the previous site signing key was not deleted", "kid", old.ID, "err", err, "event", "tv_cf_error")
		}
	}
	blob, _ := json.Marshal(storedKey{ID: k.ID, PEM: k.PEM})
	if err := c.Integration.Secrets.Put(ctx, SecretSiteSigningKey, blob, actor); err != nil {
		return SiteSecrets{}, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return SiteSecrets{}, err
	}
	sec := hex.EncodeToString(b)
	if err := c.Integration.Secrets.Put(ctx, SecretSiteHMAC, []byte(sec), actor); err != nil {
		return SiteSecrets{}, err
	}
	jwk, err := cf.PrivateJWK(k.PEM, k.ID)
	if err != nil {
		return SiteSecrets{}, err
	}
	c.log().Info("tv_public: site secrets created", "kid", k.ID, "by", actor, "event", "tv_public_secrets")
	c.audit(ctx, "tv_public.secrets", actor, k.ID, nil)
	return SiteSecrets{HomeHMAC: sec, SigningKeyID: k.ID, SigningKeyJWK: jwk}, nil
}

// siteKey is the site's signing key.
func (in *Integration) siteKey(ctx context.Context) (storedKey, error) {
	var k storedKey
	if in.Secrets == nil {
		return k, errors.New("no secret store")
	}
	raw, err := in.Secrets.Get(ctx, SecretSiteSigningKey)
	if err != nil {
		return k, err
	}
	return k, json.Unmarshal(raw, &k)
}

// SignHome is the signature of a home post: hex HMAC-SHA256 over
// "<method>\n<path>\n<unix time in ms>\n<hex sha256 of the body>" (the
// Worker checks the same, the time within 60 s, and that it is newer than the
// last post it took).
func SignHome(secret []byte, method, path string, ts int64, body []byte) string {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, secret)
	fmt.Fprintf(m, "%s\n%s\n%d\n%s", method, path, ts, hex.EncodeToString(sum[:]))
	return hex.EncodeToString(m.Sum(nil))
}

// Home post headers.
const (
	HeaderHomeTime      = "X-Maktaba-Time"
	HeaderHomeSignature = "X-Maktaba-Signature"
)

// Publisher posts the site state to the Worker every PublishEvery (and when
// woken), while the site is enabled and its secrets exist.
type Publisher struct {
	Ctl   *Controller
	HTTP  *http.Client
	Every time.Duration

	mu     sync.Mutex
	wake   chan struct{}
	once   sync.Once
	status PublishStatus
	lastTS int64 // the last post's time (ms)
}

// PublishStatus is the publisher's last result (the admin page).
type PublishStatus struct {
	LastOKAt  *time.Time `json:"last_ok_at"`
	LastError string     `json:"last_error,omitempty"`
	LastAt    *time.Time `json:"last_at"`
}

func (p *Publisher) init() {
	p.once.Do(func() { p.wake = make(chan struct{}, 1) })
}

// Wake asks for a post now.
func (p *Publisher) Wake() {
	p.init()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Status is the last result.
func (p *Publisher) Status() PublishStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Run posts until ctx ends.
func (p *Publisher) Run(ctx context.Context) {
	p.init()
	every := p.Every
	if every <= 0 {
		every = PublishEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		_ = p.PublishOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-p.wake:
		}
	}
}

// ErrSiteNotSet: the site is off, or has no address or secrets yet.
var ErrSiteNotSet = errors.New("tv_public: the site is not set up")

// PublishOnce posts the state once.
func (p *Publisher) PublishOnce(ctx context.Context) error {
	c := p.Ctl
	c.init()
	ps := LoadPublicSettings(ctx, c.DB)
	if !ps.Enabled || ps.SiteURL == "" || c.Integration == nil || c.Integration.Secrets == nil {
		return ErrSiteNotSet
	}
	sec, err := c.Integration.Secrets.Get(ctx, SecretSiteHMAC)
	if err != nil || len(sec) == 0 {
		return ErrSiteNotSet
	}
	st, err := c.SiteState(ctx)
	if err != nil {
		return p.done(err)
	}
	body, _ := json.Marshal(st)
	// milliseconds, strictly increasing: the Worker refuses a time it has
	// seen (replay), and a wake can follow a tick within the same second
	ts := c.now().UnixMilli()
	p.mu.Lock()
	if ts <= p.lastTS {
		ts = p.lastTS + 1
	}
	p.lastTS = ts
	p.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ps.SiteURL+SiteStatePath, bytes.NewReader(body))
	if err != nil {
		return p.done(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderHomeTime, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderHomeSignature, SignHome(sec, http.MethodPost, SiteStatePath, ts, body))
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return p.done(fmt.Errorf("post the site state: %w", err))
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode/100 != 2 {
		return p.done(fmt.Errorf("the site answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))))
	}
	return p.done(nil)
}

func (p *Publisher) done(err error) error {
	now := p.Ctl.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	prev := p.status.LastError
	p.status.LastAt = &now
	if err == nil {
		p.status.LastOKAt, p.status.LastError = &now, ""
		return nil
	}
	p.status.LastError = err.Error()
	if prev != p.status.LastError {
		p.Ctl.log().Warn("tv_public: publishing the site state failed", "err", err, "event", "tv_public_publish_failed")
	}
	return err
}
