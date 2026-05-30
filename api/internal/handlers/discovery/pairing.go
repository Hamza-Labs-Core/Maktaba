// Package discovery wires the pairing HTTP surface (Epic 15 stories 15.5/15.6):
//
//	POST /api/pairing/request   — TV/desktop requests a code (auth required)
//	GET  /api/pairing/status    — TV polls; 200 when consumed
//	POST /api/pairing/exchange  — phone redeems the code into device tokens
//
// QR payload is `maktaba://pair?code=ABCD-1234`.
//
// Exchange now mints a real device-bound access JWT + opaque refresh
// token (Epic 15 Story 15.5 AC: "pairing exchanges a device-bound
// refresh token"). Before this change Exchange returned only
// `{user_id, expires_at}` and the flow dead-ended — a paired phone was
// never authenticated. The token mint is behind the TokenMinter seam
// so the handler stays unit-testable without a live Postgres / key set
// (mirrors the interface-seam convention used by subscriptions and
// authz).
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

// MintedTokens is what TokenMinter returns: the credentials a freshly
// paired device uses to authenticate. AccessToken is a short-lived
// RS256 JWT; RefreshToken is the opaque, device-bound, 30-day-default
// refresh secret.
type MintedTokens struct {
	AccessToken      string
	AccessExpiresIn  int
	RefreshToken     string
	RefreshExpiresIn int
	UserID           string
}

// TokenMinter turns a consumed pairing ticket into device credentials.
// Production wires a refresh.Store + jwt key set + users.Store backed
// implementation (see NewTokenMinter). Tests pass a fake.
type TokenMinter interface {
	// Mint issues an access JWT + refresh token for userID, binding the
	// refresh token to a device described by kind/label.
	Mint(ctx context.Context, userID, deviceKind, deviceLabel string) (MintedTokens, error)
}

// Handler bundles deps. Store is the persistence interface from the
// discovery package; main wires a Postgres-backed implementation,
// tests use discovery.MemoryPairingStore. Minter is required for
// Exchange to issue tokens; when nil, Exchange degrades to the legacy
// `{user_id, expires_at}` body and signals that device login is not
// configured (503) rather than silently dead-ending.
type Handler struct {
	Store   discovery.PairingStore
	Minter  TokenMinter
	TTL     time.Duration
	NowFunc func() time.Time

	// Audit, when set, records pair.code-issued / pair.code-claimed.
	// Optional: nil disables audit (the no-DB dev path).
	Audit AuditSink
}

// AuditSink is the minimal slice of securityaudit.Writer the pairing
// path needs. Kept as an interface so the handler does not import the
// audit package transitively into tests.
type AuditSink interface {
	WritePairEvent(ctx context.Context, claimed bool, actorUserID, code string)
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
	if h.Audit != nil {
		h.Audit.WritePairEvent(r.Context(), false, p.UserID, discovery.NormalizeCode(code))
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

// exchangeRequest is the phone-side body. device_kind / device_label
// describe the device being paired so the issued refresh token's
// client_meta records what was authorised.
type exchangeRequest struct {
	Code        string `json:"code"`
	DeviceKind  string `json:"device_kind,omitempty"`
	DeviceLabel string `json:"device_label,omitempty"`
}

// exchangeResponse is the credentials the paired device uses from here
// on. This is the fix for the headline gap: the flow used to return
// only {user_id, expires_at} and a paired phone was never logged in.
type exchangeResponse struct {
	AccessToken      string `json:"access_token"`
	AccessExpiresIn  int    `json:"access_expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	UserID           string `json:"user_id"`
}

// Exchange consumes the ticket and mints device credentials.
//
// Ordering: the ticket is consumed FIRST (one-time guarantee), then
// tokens are minted. If minting fails the code is already burned —
// that is the safe failure direction (a burned code is recoverable by
// re-requesting; a re-usable code is a replay hole). The caller gets a
// 500 and simply re-runs the pairing flow.
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
	if h.Minter == nil {
		// No key set / refresh store wired — device login is not
		// configured. Fail loudly rather than dead-end with a body that
		// can't authenticate anything.
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	t, err := h.Store.Consume(r.Context(), code)
	if err != nil {
		writePairingError(w, r, err)
		return
	}
	tok, err := h.Minter.Mint(r.Context(), t.UserID, req.DeviceKind, req.DeviceLabel)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("mint device tokens"))
		return
	}
	if h.Audit != nil {
		h.Audit.WritePairEvent(r.Context(), true, t.UserID, code)
	}
	common.WriteJSON(w, r, http.StatusOK, exchangeResponse{
		AccessToken:      tok.AccessToken,
		AccessExpiresIn:  tok.AccessExpiresIn,
		RefreshToken:     tok.RefreshToken,
		RefreshExpiresIn: tok.RefreshExpiresIn,
		UserID:           t.UserID,
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
