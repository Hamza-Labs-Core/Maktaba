// Signing in a TV with a code (the device-authorization shape of RFC 8628):
//
//	POST /api/auth/device/start    — the TV asks for a code (public)
//	POST /api/auth/device/token    — the TV polls with its secret (public)
//	GET  /api/auth/device?code=    — a signed-in person sees which device asks
//	POST /api/auth/device/approve  — a signed-in person lets it in
//
// A TV has no keyboard worth typing a password on. It shows a short code and
// a QR of `<home>/link?code=…`; a person signed in on a phone or the web opens
// that page and approves, and the TV's next poll gets tokens for that person
// (never anyone else: the approver is the account). The pairing routes above
// go the other way (a signed-in device hands a code to a new one).
//
// Only the TV holds the device secret, so a code read off the screen cannot
// take the session; the code only names which TV to let in. Tickets live in
// memory for ten minutes and are spent by the poll that takes the tokens: a
// home restart mid-way means asking for a new code, which the TV does itself.
package discovery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// DeviceLinkTTL is how long a TV's code can be approved.
const DeviceLinkTTL = 10 * time.Minute

// DeviceLinkInterval is how often the TV is told to poll (seconds).
const DeviceLinkInterval = 5

// deviceLinkMaxOpen caps the codes waiting at once, so anonymous starts
// cannot grow the map without bound (the per-IP route limit is the first cap).
const deviceLinkMaxOpen = 1000

// DeviceLink is the TV sign-in surface. Minter issues the tokens (the same
// one pairing uses); nil leaves the surface answering 503.
type DeviceLink struct {
	Minter  TokenMinter
	Audit   AuditSink
	NowFunc func() time.Time

	mu       sync.Mutex
	byCode   map[string]*linkTicket // normalized user code → ticket
	bySecret map[[32]byte]*linkTicket
}

type linkTicket struct {
	code       string // normalized
	secret     [32]byte
	kind       string
	label      string
	expires    time.Time
	approvedBy string
}

// Mount attaches the TV sign-in routes to r.
func (d *DeviceLink) Mount(r chi.Router) {
	r.Post("/api/auth/device/start", d.Start)
	r.Post("/api/auth/device/token", d.Token)
	r.Get("/api/auth/device", d.Lookup)
	r.Post("/api/auth/device/approve", d.Approve)
}

type startRequest struct {
	DeviceKind  string `json:"device_kind,omitempty"`
	DeviceLabel string `json:"device_label,omitempty"`
}

type startResponse struct {
	UserCode                string `json:"user_code"`
	DeviceCode              string `json:"device_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Start gives a TV a code to show and a secret to poll with.
func (d *DeviceLink) Start(w http.ResponseWriter, r *http.Request) {
	if d.Minter == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	var req startRequest
	if r.ContentLength != 0 {
		if e := common.ReadJSON(r, &req, 1<<10); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	code, err := discovery.GenerateCode()
	if err != nil {
		httperror.Write(w, r, httperror.Internal("generate code").Wrap(err))
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		httperror.Write(w, r, httperror.Internal("generate secret").Wrap(err))
		return
	}
	secret := base64.RawURLEncoding.EncodeToString(raw[:])
	t := &linkTicket{
		code:    discovery.NormalizeCode(code),
		secret:  sha256.Sum256([]byte(secret)),
		kind:    clip(req.DeviceKind, 32),
		label:   clip(req.DeviceLabel, 80),
		expires: d.now().Add(DeviceLinkTTL),
	}

	d.mu.Lock()
	d.sweepLocked()
	if len(d.byCode) >= deviceLinkMaxOpen {
		d.mu.Unlock()
		httperror.Write(w, r, httperror.Unavailable(60))
		return
	}
	if _, taken := d.byCode[t.code]; taken {
		// 40 bits: a clash means asking again, not reusing someone's code
		d.mu.Unlock()
		httperror.Write(w, r, httperror.Unavailable(1))
		return
	}
	if d.byCode == nil {
		d.byCode = map[string]*linkTicket{}
		d.bySecret = map[[32]byte]*linkTicket{}
	}
	d.byCode[t.code] = t
	d.bySecret[t.secret] = t
	d.mu.Unlock()

	link := origin(r) + "/link"
	common.WriteJSON(w, r, http.StatusCreated, startResponse{
		UserCode:                code,
		DeviceCode:              secret,
		VerificationURI:         link,
		VerificationURIComplete: link + "?code=" + code,
		ExpiresIn:               int(DeviceLinkTTL.Seconds()),
		Interval:                DeviceLinkInterval,
	})
}

type tokenRequest struct {
	DeviceCode string `json:"device_code"`
}

// Token is the TV's poll: 202 while no one approved, 200 with the tokens
// once someone did (the ticket is spent), 410 when the code ran out or is
// unknown (the TV asks for a new one).
func (d *DeviceLink) Token(w http.ResponseWriter, r *http.Request) {
	if d.Minter == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	var req tokenRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.DeviceCode == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "device_code", Message: "required"}}))
		return
	}
	key := sha256.Sum256([]byte(req.DeviceCode))

	d.mu.Lock()
	d.sweepLocked()
	t := d.bySecret[key]
	if t == nil {
		d.mu.Unlock()
		writeGone(w, r)
		return
	}
	if t.approvedBy == "" {
		d.mu.Unlock()
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "pending", "interval": DeviceLinkInterval})
		return
	}
	// spent before minting: a second poll racing this one gets nothing
	d.removeLocked(t)
	d.mu.Unlock()

	tok, err := d.Minter.Mint(r.Context(), t.approvedBy, t.kind, t.label)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("mint device tokens").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, exchangeResponse{
		AccessToken:      tok.AccessToken,
		AccessExpiresIn:  tok.AccessExpiresIn,
		RefreshToken:     tok.RefreshToken,
		RefreshExpiresIn: tok.RefreshExpiresIn,
		UserID:           tok.UserID,
	})
}

type lookupResponse struct {
	Code        string    `json:"code"`
	DeviceKind  string    `json:"device_kind"`
	DeviceLabel string    `json:"device_label"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Lookup tells a signed-in person which device a code belongs to, so the
// approve page can say what they are letting in.
func (d *DeviceLink) Lookup(w http.ResponseWriter, r *http.Request) {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	t, ok := d.open(r.URL.Query().Get("code"))
	if !ok {
		httperror.Write(w, r, httperror.NotFound("code not found"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, lookupResponse{
		Code: displayCode(t.code), DeviceKind: t.kind, DeviceLabel: t.label, ExpiresAt: t.expires,
	})
}

type approveRequest struct {
	Code string `json:"code"`
}

// Approve lets the TV in as the person approving.
func (d *DeviceLink) Approve(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || p.UserID == "" {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req approveRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	code := discovery.NormalizeCode(req.Code)
	if code == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "code", Message: "required"}}))
		return
	}
	d.mu.Lock()
	d.sweepLocked()
	t := d.byCode[code]
	switch {
	case t == nil:
		d.mu.Unlock()
		httperror.Write(w, r, httperror.NotFound("code not found"))
		return
	case t.approvedBy != "" && t.approvedBy != p.UserID:
		d.mu.Unlock()
		httperror.Write(w, r, httperror.Conflict("", "code already approved"))
		return
	}
	t.approvedBy = p.UserID
	d.mu.Unlock()

	if d.Audit != nil {
		d.Audit.WritePairEvent(r.Context(), true, p.UserID, code)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"status": "approved"})
}

// open is the unexpired ticket for a typed code.
func (d *DeviceLink) open(code string) (linkTicket, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweepLocked()
	t := d.byCode[discovery.NormalizeCode(code)]
	if t == nil {
		return linkTicket{}, false
	}
	return *t, true
}

func (d *DeviceLink) sweepLocked() {
	now := d.now()
	for _, t := range d.byCode {
		if now.After(t.expires) {
			d.removeLocked(t)
		}
	}
}

func (d *DeviceLink) removeLocked(t *linkTicket) {
	delete(d.byCode, t.code)
	delete(d.bySecret, t.secret)
}

func (d *DeviceLink) now() time.Time {
	if d.NowFunc != nil {
		return d.NowFunc()
	}
	return time.Now().UTC()
}

func writeGone(w http.ResponseWriter, r *http.Request) {
	httperror.Write(w, r, &httperror.Error{
		Type:   "https://maktaba.dev/problems/device-code-expired",
		Title:  "code expired",
		Status: http.StatusGone,
		Detail: "ask for a new code",
	})
}

// origin is the address the TV reached the home at: the page to approve on
// is on the same web port.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func displayCode(normalized string) string {
	if len(normalized) != 8 {
		return normalized
	}
	return normalized[:4] + "-" + normalized[4:]
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
