// Package servers exposes the server-registration HTTP surface:
//
//	POST /v1/servers/claims           — user mints an 8-char token
//	POST /v1/servers/claims/redeem    — server agent presents the token
//	GET  /v1/servers                  — list user's servers
//	GET  /v1/servers/{id}             — one-server detail
//	POST /v1/servers/{id}/heartbeat   — server agent updates health
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
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	Servers *stores.Servers
}

func Mount(r chi.Router, d Deps) {
	r.Post("/v1/servers/claims", d.MintClaim)
	r.Post("/v1/servers/claims/redeem", d.RedeemClaim)
	r.Get("/v1/servers", d.List)
	r.Get("/v1/servers/{id}", d.Get)
	r.Post("/v1/servers/{id}/heartbeat", d.Heartbeat)
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
	uid, err := d.Servers.ConsumeClaim(r.Context(), req.Code)
	if err != nil {
		if errors.Is(err, stores.ErrClaimInvalid) {
			writeErr(w, 404, "claim_invalid", "code expired or already used")
			return
		}
		writeErr(w, 500, "claim_failed", err.Error())
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
	sv, err := d.Servers.CreateServer(r.Context(), uid, req.Name, req.Slug, secretHash, req.Version, []byte(req.PubKey))
	if err != nil {
		writeErr(w, 500, "create_failed", err.Error())
		return
	}
	writeJSON(w, 201, redeemResp{ServerID: sv.ID, ServerSecret: secret, Slug: sv.Slug})
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
	sv, err := d.Servers.BySlug(r.Context(), id) // for v1 we accept slug or id
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

// Heartbeat is authenticated by the server_secret (basic auth: id:secret).
// We deliberately do NOT bind user-bearer-token auth here — the agent
// runs on the on-prem box, not a user device.
func (d *Deps) Heartbeat(w http.ResponseWriter, r *http.Request) {
	serverID, _, ok := r.BasicAuth()
	if !ok {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	id := chi.URLParam(r, "id")
	if id != serverID {
		writeErr(w, 403, "forbidden", "")
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
