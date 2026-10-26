// TV sign-in by code (Epic 14): the TV shows a code, a person signed in on a phone or
// the web approves it, and the TV is signed in as that person. Typing a password with
// a remote is the alternative.
//
//	POST /api/pairing/device        public, per IP: the TV asks for a code
//	POST /api/pairing/approve       signed in: the person approves the code on the TV
//	POST /api/pairing/device/token  public, per IP: the TV polls; 202 until approved,
//	                                then its tokens (once)
//
// The TV's request writes nothing: the device code it keeps is its expiry, the key's id
// and a random part, and the code it shows is an HMAC of that under the key (DeviceKey:
// the home derives it from its signing key, so a restart keeps it). So only the TV
// holding the device code can collect an approval, and a code seen on the screen (or
// guessed) is worth nothing without it. The approval is a pairing ticket under
// "tv." + code, for the approver: Exchange normalises to upper case and never reaches it.
// A device code made under another key (a key rotation, or a home without a signing key
// restarting) gets 410 and the TV asks for a new one.
package discovery

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

const (
	// DeviceCodeTTL is how long a TV's code can be approved.
	DeviceCodeTTL = 10 * time.Minute
	// devicePollInterval is how often the TV asks (seconds).
	devicePollInterval = 2
	// deviceTicketPrefix keeps a TV approval apart from a phone pairing code.
	deviceTicketPrefix = "tv."
	// crockford is GenerateCode's alphabet.
	crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// deviceKeys is the HMAC key per Handler and its id, made on first use.
type deviceKeys struct {
	once sync.Once
	key  []byte
	id   [4]byte
}

// deviceCodeLen is a device code's bytes: expiry (8), the key's id (4), random (16).
const deviceCodeLen = 8 + 4 + 16

// MountDevice attaches the TV sign-in routes. perMin caps the public ones per IP
// (0: the defaults; negative: no limit, for tests).
func (h *Handler) MountDevice(r chi.Router, perMin int) {
	start, poll := []func(http.Handler) http.Handler(nil), []func(http.Handler) http.Handler(nil)
	if perMin >= 0 {
		s, p := 20, 60
		if perMin > 0 {
			s, p = perMin, perMin
		}
		start = append(start, mw.PerIP(s))
		poll = append(poll, mw.PerIP(p))
	}
	r.With(start...).Post("/api/pairing/device", h.DeviceStart)
	r.Post("/api/pairing/approve", h.Approve)
	r.With(poll...).Post("/api/pairing/device/token", h.DeviceToken)
}

type deviceStartResponse struct {
	// Code is what the TV shows and the person approves (XXXX-XXXX).
	Code string `json:"code"`
	// DeviceCode is the TV's own secret: it polls with it, never shows it.
	DeviceCode string    `json:"device_code"`
	ExpiresAt  time.Time `json:"expires_at"`
	// VerifyPath is the web page that approves the code (the TV makes it a QR code on
	// the address it reached the home at).
	VerifyPath string `json:"verify_path"`
	Interval   int    `json:"interval"`
}

// DeviceStart gives a TV a code to show.
func (h *Handler) DeviceStart(w http.ResponseWriter, r *http.Request) {
	exp := h.now().Add(DeviceCodeTTL)
	raw := make([]byte, deviceCodeLen)
	binary.BigEndian.PutUint64(raw, uint64(exp.Unix()))
	copy(raw[8:12], h.keys().id[:])
	if _, err := rand.Read(raw[12:]); err != nil {
		httperror.Write(w, r, httperror.Internal("device code").Wrap(err))
		return
	}
	code := h.deviceCode(raw)
	shown := code[:4] + "-" + code[4:]
	common.WriteJSON(w, r, http.StatusCreated, deviceStartResponse{
		Code:       shown,
		DeviceCode: base64.RawURLEncoding.EncodeToString(raw),
		ExpiresAt:  time.Unix(exp.Unix(), 0).UTC(),
		VerifyPath: "/pair?code=" + shown,
		Interval:   devicePollInterval,
	})
}

type approveRequest struct {
	Code string `json:"code"`
}

// Approve signs the TV showing code in as the caller (with the caller's own role: the
// tokens are minted for them when the TV collects them).
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req approveRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	code := discovery.NormalizeCode(req.Code)
	if !validDeviceCode(code) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "code", Message: "eight letters and digits, as the TV shows"},
		}))
		return
	}
	key := deviceTicketPrefix + code
	// an approval stands once (one atomic insert): approving again, as anyone, would
	// hand the TV another session or another person's
	now := h.now()
	switch err := h.Store.Insert(r.Context(), discovery.PairingTicket{
		Code: key, UserID: p.UserID, IssuedAt: now, ExpiresAt: now.Add(DeviceCodeTTL),
	}); {
	case errors.Is(err, discovery.ErrCodeExists):
		httperror.Write(w, r, httperror.Conflict("", "code already approved"))
		return
	case err != nil:
		httperror.Write(w, r, httperror.Internal("store approval").Wrap(err))
		return
	}
	if h.Audit != nil {
		h.Audit.WritePairEvent(r.Context(), false, p.UserID, key)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"status": "approved"})
}

type deviceTokenRequest struct {
	DeviceCode  string `json:"device_code"`
	DeviceLabel string `json:"device_label,omitempty"`
}

// DeviceToken answers the TV's poll: 202 while waiting, the tokens once approved (one
// time), 410 when its code has expired (or the home restarted): it asks for a new one.
func (h *Handler) DeviceToken(w http.ResponseWriter, r *http.Request) {
	var req deviceTokenRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(req.DeviceCode)
	if err != nil || len(raw) != deviceCodeLen {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "device_code", Message: "not a device code"},
		}))
		return
	}
	if h.now().Unix() > int64(binary.BigEndian.Uint64(raw)) || !hmac.Equal(raw[8:12], h.keys().id[:]) {
		httperror.Write(w, r, gone("code expired: ask for a new one"))
		return
	}
	if h.Minter == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	t, err := h.Store.Consume(r.Context(), deviceTicketPrefix+h.deviceCode(raw))
	switch {
	case errors.Is(err, discovery.ErrCodeNotFound):
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "pending", "interval": devicePollInterval})
		return
	case errors.Is(err, discovery.ErrCodeExpired):
		httperror.Write(w, r, gone("approval expired: ask for a new code"))
		return
	case err != nil:
		writePairingError(w, r, err)
		return
	}
	label := strings.TrimSpace(req.DeviceLabel)
	if len(label) > 80 {
		label = label[:80]
	}
	tok, err := h.Minter.Mint(r.Context(), t.UserID, "tv", label)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("mint device tokens").Wrap(err))
		return
	}
	if h.Audit != nil {
		h.Audit.WritePairEvent(r.Context(), true, t.UserID, t.Code)
	}
	common.WriteJSON(w, r, http.StatusOK, exchangeResponse{
		AccessToken:      tok.AccessToken,
		AccessExpiresIn:  tok.AccessExpiresIn,
		RefreshToken:     tok.RefreshToken,
		RefreshExpiresIn: tok.RefreshExpiresIn,
		UserID:           t.UserID,
	})
}

// keys is the device key (DeviceKey, else random for this process) and its id.
func (h *Handler) keys() *deviceKeys {
	h.device.once.Do(func() {
		h.device.key = h.DeviceKey
		if h.device.key == nil {
			h.device.key = make([]byte, 32)
			if _, err := rand.Read(h.device.key); err != nil {
				panic("pairing: no randomness for the device key: " + err.Error())
			}
		}
		sum := sha256.Sum256(append([]byte("maktaba tv pairing key id\x00"), h.device.key...))
		copy(h.device.id[:], sum[:4])
	})
	return &h.device
}

// DeviceKeyFrom derives the TV pairing key from a home's secret (its signing key), so
// the codes TVs wait on outlive a restart.
func DeviceKeyFrom(secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("maktaba tv pairing"))
	return m.Sum(nil)
}

// deviceCode is the eight characters a device code shows as: 40 bits of its HMAC.
func (h *Handler) deviceCode(raw []byte) string {
	m := hmac.New(sha256.New, h.keys().key)
	m.Write(raw)
	sum := m.Sum(nil)
	v := uint64(sum[0])<<32 | uint64(sum[1])<<24 | uint64(sum[2])<<16 | uint64(sum[3])<<8 | uint64(sum[4])
	var out [8]byte
	for i := 7; i >= 0; i-- {
		out[i] = crockford[v&0x1F]
		v >>= 5
	}
	return string(out[:])
}

func validDeviceCode(code string) bool {
	if len(code) != 8 {
		return false
	}
	for _, c := range code {
		if !strings.ContainsRune(crockford, c) {
			return false
		}
	}
	return true
}

func gone(detail string) *httperror.Error {
	return &httperror.Error{Type: httperror.TypeConflict, Title: "gone", Status: http.StatusGone, Detail: detail}
}
