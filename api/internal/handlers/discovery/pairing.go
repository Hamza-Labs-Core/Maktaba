// Package discovery wires the pairing HTTP surface (Epic 15 stories 15.5/15.6):
//
//	POST /api/pairing/request   — TV/desktop requests a code (auth required)
//	GET  /api/pairing/status    — TV polls; 200 when consumed
//	POST /api/pairing/exchange  — phone redeems the code into a device token
//
// QR payload is `maktaba://pair?code=ABCD-1234&host=...`.
package discovery

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Handler bundles deps. Store is the persistence interface from the
// discovery package; main wires a Postgres-backed implementation,
// tests use discovery.MemoryPairingStore.
type Handler struct {
	Store   discovery.PairingStore
	TTL     time.Duration
	NowFunc func() time.Time
}

// Mount attaches the pairing routes to r.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/pairing/request", h.Request)
	r.Get("/api/pairing/status", h.Status)
	r.Post("/api/pairing/exchange", h.Exchange)
}

// requestResponse mirrors the AC payload.
type requestResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	QRURL     string    `json:"qr_url"`
}

// Request mints a pairing ticket for the calling user.
func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	code, err := discovery.GenerateCode()
	if err != nil {
		httperror.Write(w, r, httperror.Internal("generate code"))
		return
	}
	now := h.now()
	ttl := h.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if err := h.Store.Put(r.Context(), discovery.PairingTicket{
		Code:      discovery.NormalizeCode(code),
		UserID:    p.UserID,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}); err != nil {
		httperror.Write(w, r, httperror.Internal("store ticket"))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, requestResponse{
		Code:      code,
		ExpiresAt: now.Add(ttl),
		QRURL:     "maktaba://pair?code=" + code,
	})
}

// Status returns 200 + the user id once a ticket has been consumed,
// 202 while still pending, and a problem+json on expiry / not-found.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	code := discovery.NormalizeCode(r.URL.Query().Get("code"))
	if code == "" {
		httperror.Write(w, r, httperror.Unprocessable(nil))
		return
	}
	t, err := h.Store.Get(r.Context(), code)
	if err != nil {
		writePairingError(w, r, err)
		return
	}
	if t.ConsumedAt == nil {
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "pending"})
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"status":      "paired",
		"user_id":     t.UserID,
		"consumed_at": t.ConsumedAt,
	})
}

// exchangeRequest is the phone-side body.
type exchangeRequest struct {
	Code string `json:"code"`
}

// exchangeResponse is what the phone receives.
type exchangeResponse struct {
	UserID    string `json:"user_id"`
	ExpiresAt time.Time
}

// Exchange consumes the ticket and returns the linked user id. The
// caller is expected to follow up with POST /api/devices/register to
// obtain a device-scoped access token.
func (h *Handler) Exchange(w http.ResponseWriter, r *http.Request) {
	var req exchangeRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	code := discovery.NormalizeCode(req.Code)
	if code == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "code", Message: "required"},
		}))
		return
	}
	t, err := h.Store.Consume(r.Context(), code)
	if err != nil {
		writePairingError(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"user_id":    t.UserID,
		"expires_at": t.ExpiresAt,
	})
}

func writePairingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, discovery.ErrCodeNotFound):
		httperror.Write(w, r, httperror.NotFound("code not found"))
	case errors.Is(err, discovery.ErrCodeExpired):
		httperror.Write(w, r, httperror.Conflict("", "code expired"))
	case errors.Is(err, discovery.ErrCodeConsumed):
		httperror.Write(w, r, httperror.Conflict("", "code already consumed"))
	default:
		httperror.Write(w, r, httperror.Internal("pairing"))
	}
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

// ContextFromConfig is a small helper kept for symmetry with other
// handlers; pairing is otherwise stateless.
func ContextFromConfig(ctx context.Context) context.Context { return ctx }
