// Package apitokens implements the self-service personal-access-token
// surface (web-pages-batch2 Profile → API Tokens):
//
//	GET    /api/me/tokens        → list the caller's PATs (never the raw token)
//	POST   /api/me/tokens        → create a PAT, return the raw token ONCE
//	DELETE /api/me/tokens/{id}   → revoke one of the caller's PATs
//
// All routes are owner-scoped to the request principal; there is no
// admin cross-user view here. The raw token is returned exactly once in
// the POST response and never again — the store keeps only its hash.
package apitokens

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// maxNameLen bounds the user-supplied token label.
const maxNameLen = 100

// maxExpiryDays caps how far out a token may be set to expire (~10y).
const maxExpiryDays = 3650

// Handler owns the surface.
type Handler struct {
	Store *pat.Store
}

// New builds a Handler from a store.
func New(store *pat.Store) *Handler { return &Handler{Store: store} }

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/me/tokens", h.List)
	r.Post("/api/me/tokens", h.Create)
	r.Delete("/api/me/tokens/{id}", h.Revoke)
}

// tokenView is the safe projection — no hash, no plaintext.
type tokenView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func toView(t pat.Token) tokenView {
	return tokenView{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		Scopes:     t.Scopes,
		LastUsedAt: t.LastUsedAt,
		ExpiresAt:  t.ExpiresAt,
		CreatedAt:  t.CreatedAt,
		RevokedAt:  t.RevokedAt,
	}
}

// List returns the caller's tokens.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	rows, err := h.Store.List(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list tokens"))
		return
	}
	items := make([]tokenView, 0, len(rows))
	for _, t := range rows {
		items = append(items, toView(t))
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

type createRequest struct {
	Name string `json:"name"`
	// Scopes is the coarse capability set; empty ⇒ inherit owner perms.
	Scopes []string `json:"scopes,omitempty"`
	// ExpiresInDays optionally sets a hard expiry. 0 / omitted ⇒ never.
	ExpiresInDays int `json:"expires_in_days,omitempty"`
}

// createResponse carries the raw token ONCE alongside the safe view.
type createResponse struct {
	Token string    `json:"token"`
	PAT   tokenView `json:"pat"`
}

// Create mints a token for the caller.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	var req createRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httperror.Write(w, r, httperror.BadRequest("name is required"))
		return
	}
	if len(name) > maxNameLen {
		httperror.Write(w, r, httperror.BadRequest("name is too long"))
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxExpiryDays {
		httperror.Write(w, r, httperror.BadRequest("expires_in_days out of range"))
		return
	}
	in := pat.CreateInput{UserID: p.UserID, Name: name, Scopes: req.Scopes}
	if req.ExpiresInDays > 0 {
		exp := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		in.ExpiresAt = &exp
	}
	t, err := h.Store.Create(r.Context(), in)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("create token"))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, createResponse{Token: t.Plaintext, PAT: toView(*t)})
}

// Revoke soft-revokes one of the caller's tokens.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	if err := h.Store.Revoke(r.Context(), p.UserID, id); err != nil {
		if err == pat.ErrNotFound {
			httperror.Write(w, r, httperror.NotFound("token"))
			return
		}
		httperror.Write(w, r, httperror.Internal("revoke token"))
		return
	}
	common.WriteNoContent(w)
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	httperror.Write(w, r, &httperror.Error{
		Type:   "https://maktaba.dev/problems/unauthorized",
		Title:  "unauthorized",
		Status: http.StatusUnauthorized,
	})
}
