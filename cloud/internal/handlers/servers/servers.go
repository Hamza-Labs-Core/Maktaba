// Package servers exposes the server-registration HTTP surface:
//
//	POST /v1/servers/claims           — user mints an 8-char token
//	POST /v1/servers/claims/redeem    — server agent presents the token
//	GET  /v1/servers                  — list user's servers
//	GET  /v1/servers/{id}             — one-server detail
//	POST /v1/servers/{id}/heartbeat   — server agent updates health
//	PUT  /v1/servers/{id}/links       — server agent reports its linked cloud accounts
//	GET  /v1/servers/{id}/license     — server agent fetches its owner's plan license
//	GET  /v1/licenses/revocations     — public: the signed revocation list
//
// The user routes (MountUser) sit behind the cloud's user auth; the
// agent routes (MountAgent) do not: the agent has no user token, the
// claim code or the server's own id + secret authenticate it.
package servers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	Servers *stores.Servers
	// Assertions signs home assertions (linked accounts); its public key
	// goes back to the agent at redeem, where the home pins it. Nil ⇒
	// none is sent (the agent asks again later).
	Assertions *homeassert.Signer
	// Licenses issues a home its owner's plan license (Epic 16 story
	// 16.4). Nil ⇒ the license routes answer 503.
	Licenses *license.Issuer
	// ManageURL is the billing page a home's Settings links to.
	ManageURL string
}

// MountUser registers the routes a signed-in cloud user calls.
func MountUser(r chi.Router, d Deps) {
	r.Post("/v1/servers/claims", d.MintClaim)
	r.Get("/v1/servers", d.List)
	r.Get("/v1/servers/{id}", d.Get)
}

// MountAgent registers the routes a home's agent calls, outside the
// user auth group.
func MountAgent(r chi.Router, d Deps) {
	r.Post("/v1/servers/claims/redeem", d.RedeemClaim)
	r.Post("/v1/servers/{id}/heartbeat", d.Heartbeat)
	r.Put("/v1/servers/{id}/links", d.PutLinks)
	r.Get("/v1/servers/{id}/license", d.License)
	r.Get("/v1/licenses/revocations", d.Revocations)
}

type claimResp struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MintClaim is called by an authenticated user from the web UI. The
// returned code is shown on screen for the user to type into the
// on-prem server's setup wizard.
func (d *Deps) MintClaim(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	code, err := d.Servers.MintClaim(r.Context(), uid)
	if err != nil {
		writeErr(w, 500, "mint_failed", err.Error())
		return
	}
	writeJSON(w, 200, claimResp{Code: code, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)})
}

type redeemReq struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Version string `json:"version"`
	PubKey  string `json:"public_key_pem"`
}

type redeemResp struct {
	ServerID     string `json:"server_id"`
	ServerSecret string `json:"server_secret"`
	Slug         string `json:"slug"`
	// AssertionPublicKey is the cloud's home-assertion key (base64
	// Ed25519), pinned by the home at claim.
	AssertionPublicKey string `json:"assertion_public_key,omitempty"`
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,28}[a-z0-9]$`)

// RedeemClaim is called by the server agent (no user auth — the claim
// code IS the auth). On success we register the server, mint a
// long-lived server_secret, and return both.
func (d *Deps) RedeemClaim(w http.ResponseWriter, r *http.Request) {
	var req redeemReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if len(req.Code) != 8 {
		writeErr(w, 400, "bad_code", "must be 8 chars")
		return
	}
	if !slugRE.MatchString(req.Slug) {
		writeErr(w, 422, "bad_slug", "slug must be lowercase alphanumeric, 2-30 chars")
		return
	}
	secret, err := newServerSecret()
	if err != nil {
		writeErr(w, 500, "secret_failed", err.Error())
		return
	}
	secretHash, err := argon2id.Hash(secret, argon2id.DefaultParams())
	if err != nil {
		writeErr(w, 500, "hash_failed", err.Error())
		return
	}
	sv, err := d.Servers.RedeemClaim(r.Context(), req.Code, req.Name, req.Slug, secretHash, req.Version, []byte(req.PubKey))
	switch {
	case errors.Is(err, stores.ErrClaimInvalid):
		writeErr(w, 404, "claim_invalid", "code expired or already used")
		return
	case errors.Is(err, stores.ErrSlugTaken):
		writeErr(w, 409, "slug_taken", "that subdomain is reserved or taken; the code is still valid")
		return
	case err != nil:
		writeErr(w, 500, "claim_failed", "")
		return
	}
	resp := redeemResp{ServerID: sv.ID, ServerSecret: secret, Slug: sv.Slug}
	if d.Assertions != nil {
		resp.AssertionPublicKey = d.Assertions.PublicKeyB64()
	}
	writeJSON(w, 201, resp)
}

func (d *Deps) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	servers, err := d.Servers.ListByOwner(r.Context(), uid)
	if err != nil {
		writeErr(w, 500, "list_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"servers": servers})
}

func (d *Deps) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	id := chi.URLParam(r, "id")
	sv, err := d.Servers.BySlugOrID(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "not_found", "")
		return
	}
	if sv.OwnerUserID != uid {
		writeErr(w, 404, "not_found", "")
		return
	}
	writeJSON(w, 200, sv)
}

type heartbeatReq struct {
	Online          bool    `json:"online"`
	RelayLatencyMS  int     `json:"relay_latency_ms"`
	DirectLatencyMS int     `json:"direct_latency_ms"`
	CPU             float32 `json:"cpu_pct"`
	Mem             float32 `json:"mem_pct"`
	Storage         float32 `json:"storage_pct"`
}

// agentAuth authenticates a home agent by its server id + secret (basic
// auth id:secret) for the server in the path; false after writing 401.
func (d *Deps) agentAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	serverID, secret, ok := r.BasicAuth()
	if !ok || serverID != chi.URLParam(r, "id") || d.Servers.VerifySecret(r.Context(), serverID, secret) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
		writeErr(w, 401, "unauthorized", "")
		return "", false
	}
	return serverID, true
}

// Heartbeat is authenticated by the server_secret (basic auth: id:secret).
// We deliberately do NOT bind user-bearer-token auth here — the agent
// runs on the on-prem box, not a user device.
func (d *Deps) Heartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := d.agentAuth(w, r)
	if !ok {
		return
	}
	var req heartbeatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	if err := d.Servers.Heartbeat(r.Context(), id, req.Online, req.RelayLatencyMS, req.DirectLatencyMS, req.CPU, req.Mem, req.Storage); err != nil {
		writeErr(w, 500, "update_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type linksReq struct {
	CloudUserIDs []string `json:"cloud_user_ids"`
}

// maxLinks bounds one home's report (a home has far fewer users).
const maxLinks = 10000

// PutLinks is the home's report of the cloud accounts linked to its
// users (Epic 42 story 42.7), by id only and replacing the last report.
// It lets the cloud list the home to those accounts and sign their
// sign-in assertions; the home still decides every sign-in itself.
func (d *Deps) PutLinks(w http.ResponseWriter, r *http.Request) {
	id, ok := d.agentAuth(w, r)
	if !ok {
		return
	}
	var req linksReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	if len(req.CloudUserIDs) > maxLinks {
		writeErr(w, 413, "too_many", "")
		return
	}
	if err := d.Servers.ReplaceLinks(r.Context(), id, req.CloudUserIDs); err != nil {
		writeErr(w, 500, "update_failed", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// License answers a home's agent with the license its owner's plan
// grants: 200 {license, manage_url}; 404 no_license (the free plan: the
// home returns to free); 403 license_revoked (revoked, or the account
// suspended after a disputed payment: the home locks at once).
func (d *Deps) License(w http.ResponseWriter, r *http.Request) {
	id, ok := d.agentAuth(w, r)
	if !ok {
		return
	}
	if d.Licenses == nil {
		writeErr(w, 503, "licensing_disabled", "")
		return
	}
	lic, err := d.Licenses.ForServer(r.Context(), id)
	switch {
	case errors.Is(err, license.ErrNoLicense):
		writeErr(w, 404, "no_license", "this home's owner has no paid plan")
	case errors.Is(err, license.ErrRevoked):
		writeErr(w, 403, "license_revoked", "")
	case errors.Is(err, license.ErrUnknownServer):
		writeErr(w, 404, "not_found", "")
	case err != nil:
		writeErr(w, 500, "license_failed", "")
	default:
		writeJSON(w, 200, map[string]any{"license": lic, "manage_url": d.ManageURL})
	}
}

// Revocations is the signed list of revoked license ids every home
// fetches daily; public (it names ids only).
func (d *Deps) Revocations(w http.ResponseWriter, r *http.Request) {
	if d.Licenses == nil {
		writeErr(w, 503, "licensing_disabled", "")
		return
	}
	list, err := d.Licenses.Revocations(r.Context())
	if err != nil {
		writeErr(w, 500, "revocations_failed", "")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, 200, list)
}

func newServerSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
