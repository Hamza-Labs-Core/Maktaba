// Package tvcodes signs a TV in from anywhere with a code through Maktaba Cloud (Epic 58 story
// 58.03), RFC 8628's device authorization adapted:
//
//	POST /v1/tv/codes                         public, per IP: a TV asks for a code
//	                                          {platform, app_version} -> {code, poll_secret,
//	                                          expires_in, interval, verify_url}
//	POST /v1/tv/codes/poll                    public, per IP: the TV polls {code, poll_secret}:
//	                                          202 pending, 429 slow_down, 200 the payload
//	                                          (once), then 410
//	GET  /v1/homes/{id}/tv-codes/{code}       a home (its server secret): what asked with an
//	                                          open code (platform, app version, country)
//	POST /v1/homes/{id}/tv-codes/{code}/claim a home: the code is mine; {grant} is the home's
//	                                          single-use pairing secret for the TV
//
// A person approves the code on a device signed in to the home; the home makes a pairing grant
// for that person and claims the code here. The cloud adds the home's connections (story
// 50.13), seals the payload (bound to the code) and hands it to the TV's next poll, then keeps
// nothing of it. The TV redeems the grant on the home (POST /api/pairing/device/token), so the
// home's own sign-in rules decide everything after. No cloud account is involved: the home's
// registration is enough.
package tvcodes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/homes"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

const (
	// CodeTTL is how long a code can be approved.
	CodeTTL = 10 * time.Minute
	// Interval is how often a TV polls.
	Interval = 5 * time.Second
	// SweepAfter is how long past expiry a row stays before the sweep deletes it.
	SweepAfter = time.Hour
	// Alphabet is the codes' characters: story 15.06's pairing alphabet (Crockford: no I L O U)
	// without 0 and 1, which read like O and I on a TV.
	Alphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"
	// CodeLen is a code's characters (shown ABCD-1234).
	CodeLen = 8
	// MaxOpenPerIP is the codes one address may ask for in a burst; one more each
	// CodeTTL/MaxOpenPerIP after that, so an address never holds more than this many open.
	MaxOpenPerIP = 10
	// DefaultVerifyURL is the page a phone's camera opens from the TV's QR code.
	DefaultVerifyURL = "https://maktaba.tv/tv"
)

// Servers checks a home's secret and reads its row.
type Servers interface {
	VerifySecret(ctx context.Context, serverID, secret string) error
	ByID(ctx context.Context, id string) (stores.Server, error)
}

// Reacher is each home's connections (homes.Deps).
type Reacher interface {
	ReachFor(ctx context.Context, hs []stores.Server) map[string]homes.Reach
}

// Deps wires the handlers.
type Deps struct {
	Store   Store
	Servers Servers
	Reach   Reacher
	// Box seals each payload at rest (the cloud's secret box, under a key of its own).
	Box *staffauth.Box
	// Trusted are the proxies whose CF-IPCountry is believed (server.trusted_proxies).
	Trusted []netip.Prefix
	// VerifyURL is the approve page the QR code opens (DefaultVerifyURL when empty).
	VerifyURL string
	// CreateLimit caps the codes per address (a fresh MaxOpenPerIP limiter when nil);
	// PollLimit the polls (burst 60, one a second, when nil).
	CreateLimit *ratelimit.Limiter
	PollLimit   *ratelimit.Limiter
	// AgentLimit caps each home's lookups and claims, counted after its secret is checked
	// (so no one else spends a home's allowance): burst 30, then one every 6 s, when nil. A
	// home approving a TV makes two calls; a home trying codes it was not given runs out.
	AgentLimit *ratelimit.Limiter
	// Approving on maktaba.tv (approve.go): the account's homes, the assertion signer, the call
	// to a home through the relay; any nil: 503. UserLimit caps each account's lookups and
	// approvals (burst 20, then one every 3 s, when nil).
	Homes     Homes
	Signer    Asserter
	Caller    HomeCaller
	UserLimit *ratelimit.Limiter
	Now       func() time.Time
	Logger    *slog.Logger
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// BoxKey derives the payload box's key from the cloud's token secret, which every api process
// shares: a payload sealed by one process opens in another.
func BoxKey(tokenSecret string) []byte {
	sum := sha256.Sum256([]byte("maktaba tv codes payload box\x00" + tokenSecret))
	return sum[:]
}

// MountPublic registers the TV's routes (no account), each limited per address.
func MountPublic(r chi.Router, d *Deps) {
	if d.CreateLimit == nil {
		d.CreateLimit = ratelimit.NewLimiter(MaxOpenPerIP, MaxOpenPerIP/CodeTTL.Seconds())
	}
	if d.PollLimit == nil {
		d.PollLimit = ratelimit.NewLimiter(60, 1)
	}
	r.With(d.CreateLimit.Middleware(ratelimit.IPKey)).Post("/v1/tv/codes", d.Create)
	r.With(d.PollLimit.Middleware(ratelimit.IPKey)).Post("/v1/tv/codes/poll", d.Poll)
}

// MountAgent registers a home's routes (its server secret is the credential).
func MountAgent(r chi.Router, d *Deps) {
	if d.AgentLimit == nil {
		d.AgentLimit = ratelimit.NewLimiter(30, 1.0/6)
	}
	r.Get("/v1/homes/{id}/tv-codes/{code}", d.Lookup)
	r.Post("/v1/homes/{id}/tv-codes/{code}/claim", d.Claim)
}

type createReq struct {
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
}

type createResp struct {
	Code       string `json:"code"`
	PollSecret string `json:"poll_secret"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
	VerifyURL  string `json:"verify_url"`
}

// Create gives a TV a code and its poll secret.
func (d *Deps) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "")
		return
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		writeErr(w, http.StatusInternalServerError, "no_randomness", "")
		return
	}
	pollSecret := base64.RawURLEncoding.EncodeToString(secret)
	now := d.now().UTC().Truncate(time.Second)
	row := Row{Platform: clean(req.Platform, 32), AppVersion: clean(req.AppVersion, 32),
		Country: country(r, d.Trusted), ExpiresAt: now.Add(CodeTTL)}
	for range 5 {
		code, err := newCode()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "no_randomness", "")
			return
		}
		row.Code = code
		switch err := d.Store.Create(r.Context(), row, hashSecret(pollSecret), now); {
		case errors.Is(err, ErrExists):
			continue
		case err != nil:
			d.log().Error("tv codes: create failed", "err", err, "event", "tv_code_create_failed")
			writeErr(w, http.StatusInternalServerError, "create_failed", "")
			return
		}
		verify := d.VerifyURL
		if verify == "" {
			verify = DefaultVerifyURL
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, createResp{Code: Shown(code), PollSecret: pollSecret,
			ExpiresIn: int(CodeTTL.Seconds()), Interval: int(Interval.Seconds()), VerifyURL: verify + "?c=" + code})
		return
	}
	writeErr(w, http.StatusServiceUnavailable, "try_again", "")
}

type pollReq struct {
	Code       string `json:"code"`
	PollSecret string `json:"poll_secret"`
}

type pollResp struct {
	Status      string             `json:"status"`
	HomeID      string             `json:"home_id"`
	HomeName    string             `json:"home_name"`
	Connections []homes.Connection `json:"connections"`
	Grant       string             `json:"grant"`
}

// Payload is what a home's claim leaves for its TV (sealed at rest).
type Payload struct {
	HomeID      string             `json:"home_id"`
	HomeName    string             `json:"home_name"`
	Connections []homes.Connection `json:"connections"`
	Grant       string             `json:"grant"`
}

// Poll answers the TV: pending, slow_down, the payload (once), or gone.
func (d *Deps) Poll(w http.ResponseWriter, r *http.Request) {
	var req pollReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "")
		return
	}
	code := Normalize(req.Code)
	w.Header().Set("Cache-Control", "no-store")
	if !Valid(code) || req.PollSecret == "" {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	res, sealed, err := d.Store.Poll(r.Context(), code, hashSecret(req.PollSecret), d.now().UTC(), Interval)
	if err != nil {
		d.log().Error("tv codes: poll failed", "err", err, "event", "tv_code_poll_failed")
		writeErr(w, http.StatusInternalServerError, "poll_failed", "")
		return
	}
	switch res {
	case PollPending:
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending", "interval": int(Interval.Seconds())})
	case PollSlowDown:
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "slow_down", "interval": int(Interval.Seconds()) + 5})
	case PollGone:
		writeErr(w, http.StatusGone, "expired_token", "the code expired or was used: ask for a new one")
	case PollNotFound:
		writeErr(w, http.StatusNotFound, "not_found", "")
	case PollDelivered:
		plain, err := d.Box.Open(string(sealed), boxWhat(code))
		var p Payload
		if err == nil {
			err = json.Unmarshal([]byte(plain), &p)
		}
		if err != nil {
			// delivered and cleared, but unreadable: the TV asks for a new code
			d.log().Error("tv codes: a payload did not open", "err", err, "event", "tv_code_payload_unreadable")
			writeErr(w, http.StatusGone, "expired_token", "ask for a new code")
			return
		}
		writeJSON(w, http.StatusOK, pollResp{Status: "approved", HomeID: p.HomeID, HomeName: p.HomeName,
			Connections: p.Connections, Grant: p.Grant})
	}
}

type lookupResp struct {
	Code       string    `json:"code"`
	Platform   string    `json:"platform"`
	AppVersion string    `json:"app_version"`
	Country    string    `json:"country"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func view(r Row) lookupResp {
	return lookupResp{Code: Shown(r.Code), Platform: r.Platform, AppVersion: r.AppVersion, Country: r.Country,
		ExpiresAt: r.ExpiresAt.UTC()}
}

// home is the server a request authenticates as (basic auth, the URL's id).
func (d *Deps) home(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, secret, ok := r.BasicAuth()
	if !ok || id != chi.URLParam(r, "id") || d.Servers.VerifySecret(r.Context(), id, secret) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return "", false
	}
	if d.AgentLimit != nil && !d.AgentLimit.Allow("home:"+id) {
		d.log().Warn("tv codes: a home is over its lookups", "server_id", id, "event", "tv_code_agent_limited")
		w.Header().Set("Retry-After", strconv.Itoa(d.AgentLimit.RetryAfter()))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "")
		return "", false
	}
	return id, true
}

// Lookup tells a home what asked with an open code, so the approver sees it before approving.
func (d *Deps) Lookup(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.home(w, r); !ok {
		return
	}
	code := Normalize(chi.URLParam(r, "code"))
	if !Valid(code) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	row, err := d.Store.Open(r.Context(), code, d.now().UTC())
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "lookup_failed", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view(row))
}

type claimReq struct {
	Grant string `json:"grant"`
}

var grantShape = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// Claim gives an open code to the home with its grant: the TV's next poll collects it.
func (d *Deps) Claim(w http.ResponseWriter, r *http.Request) {
	serverID, ok := d.home(w, r)
	if !ok {
		return
	}
	code := Normalize(chi.URLParam(r, "code"))
	var req claimReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "")
		return
	}
	if !grantShape.MatchString(req.Grant) {
		writeErr(w, http.StatusUnprocessableEntity, "bad_grant", "")
		return
	}
	if !Valid(code) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	sv, err := d.Servers.ByID(r.Context(), serverID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	p := Payload{HomeID: sv.ID, HomeName: sv.Name, Grant: req.Grant, Connections: []homes.Connection{}}
	if d.Reach != nil {
		if rc, ok := d.Reach.ReachFor(r.Context(), []stores.Server{sv})[sv.ID]; ok && rc.Connections != nil {
			p.Connections = rc.Connections
		}
	}
	plain, _ := json.Marshal(p)
	sealed, err := d.Box.Seal(string(plain), boxWhat(code))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "seal_failed", "")
		return
	}
	row, err := d.Store.Claim(r.Context(), code, sv.ID, []byte(sealed), d.now().UTC())
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	case errors.Is(err, ErrClaimed):
		writeErr(w, http.StatusConflict, "already_claimed", "")
		return
	case err != nil:
		d.log().Error("tv codes: claim failed", "err", err, "event", "tv_code_claim_failed")
		writeErr(w, http.StatusInternalServerError, "claim_failed", "")
		return
	}
	d.log().Info("tv codes: claimed", "server_id", sv.ID, "platform", row.Platform, "event", "tv_code_claimed")
	writeJSON(w, http.StatusOK, view(row))
}

// RunSweeper deletes the rows an hour past their expiry, every ten minutes until ctx ends.
func RunSweeper(ctx context.Context, s Store, logger *slog.Logger) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if n, err := s.Sweep(ctx, time.Now().Add(-SweepAfter)); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("tv codes: the sweep failed", "err", err, "event", "tv_codes_sweep_failed")
		} else if n > 0 && logger != nil {
			logger.Info("tv codes: swept", "deleted", n, "event", "tv_codes_swept")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (d *Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// newCode is CodeLen random characters of Alphabet.
func newCode() (string, error) {
	var b strings.Builder
	n := big.NewInt(int64(len(Alphabet)))
	for range CodeLen {
		i, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b.WriteByte(Alphabet[i.Int64()])
	}
	return b.String(), nil
}

// Normalize upper-cases a typed code and drops its dash and spaces.
func Normalize(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}

// Valid is whether code (normalized) is CodeLen characters of Alphabet.
func Valid(code string) bool {
	if len(code) != CodeLen {
		return false
	}
	for _, c := range code {
		if !strings.ContainsRune(Alphabet, c) {
			return false
		}
	}
	return true
}

// Shown is a code as a TV shows it: ABCD-1234.
func Shown(code string) string {
	if len(code) != CodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

func hashSecret(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func boxWhat(code string) string { return "tv-code:" + code }

// clean is s without control characters, at most n bytes (cut on a rune boundary).
func clean(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

var countryShape = regexp.MustCompile(`^[A-Z]{2}$`)

// country is the request's country as the edge saw it (CF-IPCountry), only from a trusted
// proxy; "" otherwise, and for Cloudflare's XX (unknown) and T1 (Tor).
func country(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	peer = peer.Unmap()
	ok := false
	for _, p := range trusted {
		if p.Contains(peer) {
			ok = true
			break
		}
	}
	c := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
	if !ok || !countryShape.MatchString(c) || c == "XX" || c == "T1" {
		return ""
	}
	return c
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
