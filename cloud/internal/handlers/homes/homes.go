// Package homes is a cloud account's view of the homes it reaches
// (Epic 42 story 42.7, linked accounts):
//
//	GET  /v1/homes                 — the homes this account is linked to or owns
//	POST /v1/homes/{id}/assertion  — a one-use home assertion for one of them
//	GET  /v1/homes/assertion-key   — the cloud's assertion public key (public)
//
// An assertion names the cloud account and the home, for one use, a
// minute; it never says what the account may do there. The home
// verifies it, finds the home user it linked, and issues its own
// tokens with that user's role and the link's scope.
package homes

import (
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
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Deps wires the handlers.
type Deps struct {
	Servers *stores.Servers
	Users   *stores.Users
	// Signer signs assertions; nil ⇒ the cloud has no assertion key and
	// answers 503 (linked sign-in unavailable).
	Signer *homeassert.Signer
	// RelayHost is the relay's public apex: a home's relay address is
	// https://<slug>.<RelayHost>.
	RelayHost string
}

// MountUser registers the routes behind the user auth group.
func MountUser(r chi.Router, d *Deps) {
	r.Get("/v1/homes", d.List)
	r.Post("/v1/homes/{id}/assertion", d.Assertion)
}

// MountPublic registers the public key route (no auth).
func MountPublic(r chi.Router, d *Deps) {
	r.Get("/v1/homes/assertion-key", d.Key)
}

type homeView struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	// RelayURL is the home's address through the relay; DirectURL its
	// direct address when it reported one (story 25.10: try it first).
	RelayURL  string `json:"relay_url"`
	DirectURL string `json:"direct_url,omitempty"`
	Online    bool   `json:"online"`
	// Linked: the home linked this account to one of its users (sign-in
	// works). Owned: this account holds the home's cloud registration,
	// which by itself is no account inside the home.
	Linked bool `json:"linked"`
	Owned  bool `json:"owned"`
}

// List answers the homes this cloud account reaches.
func (d *Deps) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	hs, err := d.Servers.HomesFor(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	out := make([]homeView, 0, len(hs))
	for _, h := range hs {
		v := homeView{ServerID: h.ID, Name: h.Name, Slug: h.Slug, Online: h.Online, Linked: h.Linked, Owned: h.Owned}
		if d.RelayHost != "" {
			v.RelayURL = "https://" + h.Slug + "." + d.RelayHost
		}
		if h.DirectIP.Valid && h.DirectPort.Valid {
			host := h.DirectIP.String
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			v.DirectURL = "http://" + host + ":" + strconv.FormatInt(h.DirectPort.Int64, 10)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"homes": out})
}

type assertionReq struct {
	// Purpose is "signin" (default) or "link".
	Purpose string `json:"purpose"`
	// Nonce is the home's link nonce (purpose link).
	Nonce string `json:"nonce"`
}

type assertionResp struct {
	Assertion string    `json:"assertion"`
	ExpiresAt time.Time `json:"expires_at"`
	ServerID  string    `json:"server_id"`
	RelayURL  string    `json:"relay_url,omitempty"`
}

// Assertion signs a one-use assertion for the caller and one home. A
// sign-in assertion is only for a home that linked this account; a link
// assertion is for any home (the home binds it to the home user who
// started the link with its nonce, and refuses it otherwise).
func (d *Deps) Assertion(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	if d.Signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "assertions_unavailable", "this cloud has no assertion key")
		return
	}
	var req assertionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid_body", "")
		return
	}
	if req.Purpose == "" {
		req.Purpose = homeassert.PurposeSignIn
	}
	if req.Purpose != homeassert.PurposeSignIn && req.Purpose != homeassert.PurposeLink {
		writeErr(w, http.StatusUnprocessableEntity, "bad_purpose", "purpose is signin or link")
		return
	}
	if req.Purpose == homeassert.PurposeLink && (len(req.Nonce) < 16 || len(req.Nonce) > 128) {
		writeErr(w, http.StatusUnprocessableEntity, "bad_nonce", "a link needs the home's nonce")
		return
	}
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil || u.Status != "active" {
		writeErr(w, http.StatusForbidden, "account_inactive", "")
		return
	}
	sv, err := d.Servers.BySlugOrID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	label := ""
	if req.Purpose == homeassert.PurposeSignIn {
		linked, err := d.Servers.IsLinked(r.Context(), sv.ID, uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "lookup_failed", "")
			return
		}
		if !linked {
			// the same answer as an unknown home: nothing about a home the
			// account is not linked to
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
	} else {
		label = u.Email
	}
	tok, p, err := d.Signer.Issue(req.Purpose, uid, sv.ID, req.Nonce, label)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sign_failed", "")
		return
	}
	resp := assertionResp{Assertion: tok, ExpiresAt: time.Unix(p.Exp, 0).UTC(), ServerID: sv.ID}
	if d.RelayHost != "" {
		resp.RelayURL = "https://" + sv.Slug + "." + d.RelayHost
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// Key publishes the assertion public key. A home pins the key it got at
// claim; one claimed before assertions existed fetches it here once.
func (d *Deps) Key(w http.ResponseWriter, _ *http.Request) {
	if d.Signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "assertions_unavailable", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"alg": "Ed25519", "public_key": d.Signer.PublicKeyB64()})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
