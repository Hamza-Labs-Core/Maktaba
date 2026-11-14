package tvcodes

// Approving a TV's code on maktaba.tv (story 58.03): a person signed in to Maktaba Cloud, whose
// account a home linked to one of its users (Epic 42 story 42.7), approves the code for that home:
//
//	GET  /v1/tv/codes/{code}          signed in: what asked with an open code (platform, country)
//	POST /v1/tv/codes/{code}/approve  signed in: approve it on one of the account's linked homes
//	                                  {server_id}
//
// The cloud does not approve anything itself: it signs a one-use assertion naming the account,
// the home and the code (purpose tvcode, homeassert) and sends it to the home through the relay
// (POST /api/pairing/cloud-assertion). The home finds the user it linked to that account and
// approves the code as them, claiming it here as any approval does; the TV it signs in is
// restricted, as every cloud code's TV is. A home that is offline, or that the relay cannot
// reach, approves nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Homes is the homes a cloud account reaches (stores.Servers.HomesFor).
type Homes interface {
	HomesFor(ctx context.Context, cloudUserID string) ([]stores.LinkedHome, error)
}

// Asserter signs home assertions (homeassert.Signer).
type Asserter interface {
	Issue(purpose, cloudUserID, serverID, nonce, label string) (string, homeassert.Payload, error)
}

// HomeAnswer is what a home said to an approval.
type HomeAnswer struct {
	Status int
	Body   []byte
}

// HomeCaller sends an approval to a home (RelayCaller: through the relay).
type HomeCaller interface {
	ApproveOnHome(ctx context.Context, slug, code, assertion string) (HomeAnswer, error)
}

// RelayCaller reaches a home's API at https://<slug>.<Host>, the relay address apps use.
type RelayCaller struct {
	// Host is the relay's public apex (relay.public_host).
	Host string
	// Base replaces https://<slug>.<Host> (tests, the rehearsal's edge); nil: that address.
	Base   func(slug string) string
	Client *http.Client
}

// approveTimeout bounds the whole round trip: the home claims the code here before it answers.
const approveTimeout = 25 * time.Second

// ApproveOnHome posts the assertion to the home's cloud-assertion route.
func (c RelayCaller) ApproveOnHome(ctx context.Context, slug, code, assertion string) (HomeAnswer, error) {
	base := "https://" + slug + "." + c.Host
	if c.Base != nil {
		base = c.Base(slug)
	} else if c.Host == "" {
		return HomeAnswer{}, errors.New("tvcodes: no relay host")
	}
	b, _ := json.Marshal(map[string]string{"code": code, "assertion": assertion})
	ctx, cancel := context.WithTimeout(ctx, approveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/pairing/cloud-assertion", bytes.NewReader(b))
	if err != nil {
		return HomeAnswer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	cl := c.Client
	if cl == nil {
		cl = &http.Client{Timeout: approveTimeout}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return HomeAnswer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return HomeAnswer{}, err
	}
	return HomeAnswer{Status: resp.StatusCode, Body: body}, nil
}

// MountUser registers the routes of a person signed in to the cloud (behind RequireUser), each
// limited per account.
func MountUser(r chi.Router, d *Deps) {
	if d.UserLimit == nil {
		// a person reads a code, then approves it: twenty, then one every three seconds
		d.UserLimit = ratelimit.NewLimiter(20, 1.0/3)
	}
	r.Get("/v1/tv/codes/{code}", d.UserLookup)
	r.Post("/v1/tv/codes/{code}/approve", d.WebApprove)
}

// account is the signed-in cloud account, within its allowance.
func (d *Deps) account(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return "", false
	}
	if d.UserLimit != nil && !d.UserLimit.Allow("user:"+uid) {
		w.Header().Set("Retry-After", strconv.Itoa(d.UserLimit.RetryAfter()))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "")
		return "", false
	}
	return uid, true
}

// open is the open code the URL names, or an answer saying there is none.
func (d *Deps) open(w http.ResponseWriter, r *http.Request) (Row, bool) {
	code := Normalize(chi.URLParam(r, "code"))
	if !Valid(code) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return Row{}, false
	}
	row, err := d.Store.Open(r.Context(), code, d.now().UTC())
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "")
		return Row{}, false
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "lookup_failed", "")
		return Row{}, false
	}
	return row, true
}

// UserLookup tells a signed-in person what asked with an open code, for "Is this your TV?".
func (d *Deps) UserLookup(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.account(w, r); !ok {
		return
	}
	row, ok := d.open(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view(row))
}

type webApproveReq struct {
	ServerID string `json:"server_id"`
}

type webApproveResp struct {
	Status   string `json:"status"`
	Platform string `json:"platform"`
	Country  string `json:"country"`
	HomeName string `json:"home_name"`
}

// WebApprove asks one of the account's linked homes to approve the code as the linked user.
func (d *Deps) WebApprove(w http.ResponseWriter, r *http.Request) {
	uid, ok := d.account(w, r)
	if !ok {
		return
	}
	var req webApproveReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.ServerID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_body", "server_id is required")
		return
	}
	row, ok := d.open(w, r)
	if !ok {
		return
	}
	if d.Homes == nil || d.Signer == nil || d.Caller == nil {
		writeErr(w, http.StatusServiceUnavailable, "approve_unavailable", "")
		return
	}
	hs, err := d.Homes.HomesFor(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "lookup_failed", "")
		return
	}
	var home *stores.LinkedHome
	for i := range hs {
		if hs[i].ID == req.ServerID {
			home = &hs[i]
		}
	}
	// only a home that linked this account has a user to approve as; owning the registration is
	// not enough, and nothing is said about a home the account does not reach
	if home == nil || !home.Linked {
		writeErr(w, http.StatusForbidden, "not_linked", "")
		return
	}
	if !home.Online {
		writeErr(w, http.StatusConflict, "home_offline", "")
		return
	}
	code := Normalize(chi.URLParam(r, "code"))
	assertion, _, err := d.Signer.Issue(homeassert.PurposeTVCode, uid, home.ID, code, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sign_failed", "")
		return
	}
	ans, err := d.Caller.ApproveOnHome(r.Context(), home.Slug, code, assertion)
	if err != nil {
		d.log().Warn("tv codes: a home could not be reached to approve", "server_id", home.ID, "err", err,
			"event", "tv_code_web_unreachable")
		writeErr(w, http.StatusServiceUnavailable, "home_unreachable", "")
		return
	}
	var said struct {
		Platform string `json:"platform"`
		Country  string `json:"country"`
		Code     string `json:"code"`
	}
	_ = json.Unmarshal(ans.Body, &said)
	switch {
	case ans.Status == http.StatusOK:
		d.log().Info("tv codes: approved on maktaba.tv", "server_id", home.ID, "event", "tv_code_web_approved")
		writeJSON(w, http.StatusOK, webApproveResp{Status: "approved", Platform: row.Platform, Country: row.Country,
			HomeName: home.Name})
	case ans.Status == http.StatusForbidden:
		// the home refused the assertion: its link to this account is gone, or its user is
		writeErr(w, http.StatusForbidden, "not_linked", "")
	case ans.Status == http.StatusNotFound:
		writeErr(w, http.StatusNotFound, "not_found", "")
	case ans.Status == http.StatusConflict && said.Code != "cloud_not_linked":
		writeErr(w, http.StatusConflict, "already_approved", "")
	default:
		// the relay's own 502/503/504 for a home that dropped, or a home that cannot reach us
		d.log().Warn("tv codes: a home did not approve", "server_id", home.ID, "status", ans.Status,
			"event", "tv_code_web_unreachable")
		writeErr(w, http.StatusServiceUnavailable, "home_unreachable", "")
	}
}
