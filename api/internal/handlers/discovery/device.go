// Sign-in by code (Epic 14 for the TVs, Epic 42 story 42.6 for every app): the app
// shows a code, a person signed in on a phone or the web sees what is asking and
// approves it, and the app is signed in as that person. Typing a password is the
// alternative.
//
//	POST /api/pairing/device          public, per IP: an app asks for a code
//	                                  (optional body {"device_label": "..."}; what is
//	                                  asking comes from X-Maktaba-Client)
//	GET  /api/pairing/device/{code}   signed in: what is asking with that code
//	POST /api/pairing/approve         signed in: the person approves the code
//	POST /api/pairing/device/token    public, per IP: the app polls; 202 until approved,
//	                                  then its tokens (once)
//
// The device code the app keeps is its expiry, the key's id and a random part, and the
// code it shows is an HMAC of that under the key (DeviceKey: the home derives it from
// its signing key). So only the app holding the device code can collect an approval,
// and a code seen on the screen (or guessed) is worth nothing without it. What asked
// (its app, platform, device and label) is kept in this process until the code expires
// (deviceRequests): the approver sees it before approving, and a code this home does
// not hold (never issued, expired, or from before a restart) cannot be approved; its
// app gets 410 and asks for a new one. The approval is a pairing ticket under
// "device." + code ("tv." + code from before story 42.6 is still collected), for the
// approver: Exchange normalises to upper case and never reaches it. A device code made
// under another key (a key rotation) gets 410 too.
package discovery

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

const (
	// DeviceCodeTTL is how long an app's code can be approved.
	DeviceCodeTTL = 10 * time.Minute
	// devicePollInterval is how often the app asks (seconds).
	devicePollInterval = 2
	// deviceTicketPrefix keeps an app's approval apart from a phone pairing code.
	deviceTicketPrefix = "device."
	// legacyTicketPrefix is a TV approval written before story 42.6 (still collected).
	legacyTicketPrefix = "tv."
	// legacyDeviceKind is the device kind of an app that does not say what it is (the
	// TVs from before X-Maktaba-Client).
	legacyDeviceKind = "tv"
	// crockford is GenerateCode's alphabet.
	crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	// maxDeviceRequests bounds the codes waiting at once (each start is rate-limited
	// per IP; past this many the home asks to try again later).
	maxDeviceRequests = 10000
	// maxDeviceLabel bounds a device label (bytes, cut on a rune boundary).
	maxDeviceLabel = 80
)

// deviceKeys is the HMAC key per Handler and its id, made on first use.
type deviceKeys struct {
	once sync.Once
	key  []byte
	id   [4]byte
}

// deviceCodeLen is a device code's bytes: expiry (8), the key's id (4), random (16).
const deviceCodeLen = 8 + 4 + 16

// deviceRequest is what asked for a code: its app, platform and device (from
// X-Maktaba-Client) and the label it gave, until the code expires.
type deviceRequest struct {
	Client  client.Info
	Label   string
	Expires time.Time
}

// deviceRequests are the codes waiting for an approval, by code (eight characters, no
// dash).
type deviceRequests struct {
	mu sync.Mutex
	m  map[string]deviceRequest
}

// put records a code's request; false when too many codes are waiting.
func (d *deviceRequests) put(code string, req deviceRequest, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]deviceRequest{}
	}
	if len(d.m) >= maxDeviceRequests {
		for c, r := range d.m {
			if now.After(r.Expires) {
				delete(d.m, c)
			}
		}
		if len(d.m) >= maxDeviceRequests {
			return false
		}
	}
	d.m[code] = req
	return true
}

// get is a code's request while the code has not expired.
func (d *deviceRequests) get(code string, now time.Time) (deviceRequest, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.m[code]
	if !ok || now.After(r.Expires) {
		return deviceRequest{}, false
	}
	return r, true
}

// drop forgets a code (its app collected its tokens).
func (d *deviceRequests) drop(code string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.m, code)
}

// MountDevice attaches the sign-in-by-code routes. perMin caps the public ones per IP
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
	r.Get("/api/pairing/device/{code}", h.DeviceLookup)
	r.Post("/api/pairing/approve", h.Approve)
	r.With(poll...).Post("/api/pairing/device/token", h.DeviceToken)
}

type deviceStartRequest struct {
	DeviceLabel string `json:"device_label,omitempty"`
}

type deviceStartResponse struct {
	// Code is what the app shows and the person approves (XXXX-XXXX).
	Code string `json:"code"`
	// DeviceCode is the app's own secret: it polls with it, never shows it.
	DeviceCode string    `json:"device_code"`
	ExpiresAt  time.Time `json:"expires_at"`
	// VerifyPath is the web page that approves the code, on any of the home's addresses.
	VerifyPath string `json:"verify_path"`
	// VerifyURL is that page on the home's first address (absolute): the app shows it as
	// a QR code, so a phone's camera opens the approve page.
	VerifyURL string `json:"verify_url"`
	Interval  int    `json:"interval"`
}

// DeviceStart gives an app a code to show, and keeps what asked for it.
func (h *Handler) DeviceStart(w http.ResponseWriter, r *http.Request) {
	var body deviceStartRequest
	if e := readOptionalJSON(r, &body); e != nil {
		httperror.Write(w, r, e)
		return
	}
	now := h.now()
	exp := now.Add(DeviceCodeTTL)
	raw := make([]byte, deviceCodeLen)
	binary.BigEndian.PutUint64(raw, uint64(exp.Unix()))
	copy(raw[8:12], h.keys().id[:])
	if _, err := rand.Read(raw[12:]); err != nil {
		httperror.Write(w, r, httperror.Internal("device code").Wrap(err))
		return
	}
	code := h.deviceCode(raw)
	expires := time.Unix(exp.Unix(), 0).UTC()
	if !h.requests.put(code, deviceRequest{
		Client:  client.Parse(r.Header.Get(client.Header)),
		Label:   cleanLabel(body.DeviceLabel),
		Expires: expires,
	}, now) {
		httperror.Write(w, r, httperror.Unavailable(30))
		return
	}
	shown := code[:4] + "-" + code[4:]
	verifyPath := "/pair?code=" + shown
	verifyURL := ""
	if urls := h.home(r).URLs; len(urls) > 0 {
		verifyURL = urls[0] + verifyPath
	}
	common.WriteJSON(w, r, http.StatusCreated, deviceStartResponse{
		Code:       shown,
		DeviceCode: base64.RawURLEncoding.EncodeToString(raw),
		ExpiresAt:  expires,
		VerifyPath: verifyPath,
		VerifyURL:  verifyURL,
		Interval:   devicePollInterval,
	})
}

type deviceLookupResponse struct {
	Code        string    `json:"code"`
	ClientApp   string    `json:"client_app"`
	Platform    string    `json:"platform"`
	Device      string    `json:"device"`
	DeviceLabel string    `json:"device_label"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// DeviceLookup says what is asking with a code, so the approver knows what they are
// approving: any signed-in person who has the code; 404 for a code this home does not
// hold (never issued, expired, collected, or from before a restart).
func (h *Handler) DeviceLookup(w http.ResponseWriter, r *http.Request) {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	code := discovery.NormalizeCode(chi.URLParam(r, "code"))
	req, ok := h.requests.get(code, h.now())
	if !validDeviceCode(code) || !ok {
		httperror.Write(w, r, codeNotHeld())
		return
	}
	common.WriteJSON(w, r, http.StatusOK, deviceLookupResponse{
		Code:        code[:4] + "-" + code[4:],
		ClientApp:   known(req.Client.App),
		Platform:    known(req.Client.Platform),
		Device:      known(req.Client.Device),
		DeviceLabel: req.Label,
		ExpiresAt:   req.Expires,
	})
}

type approveRequest struct {
	Code string `json:"code"`
}

// Approve signs the app showing code in as the caller, with the caller's own account
// and role (the tokens are minted for them when the app collects them): there is no one
// else to name, so a viewer approves for themselves only.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	// an app's tokens outlive a cloud link and carry the person's full role (Epic 42 story
	// 42.7, as for a phone's pairing): only a sign-in on the home approves one
	if p.ViaCloud() {
		httperror.Write(w, r, httperror.Forbidden("https://maktaba.tv/problems/cloud-session",
			"a sign-in through Maktaba Cloud cannot sign in a device; sign in on the home"))
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
			{Field: "code", Message: "eight letters and digits, as the device shows"},
		}))
		return
	}
	now := h.now()
	if _, ok := h.requests.get(code, now); !ok {
		httperror.Write(w, r, codeNotHeld())
		return
	}
	key := deviceTicketPrefix + code
	// an approval stands once (one atomic insert): approving again, as anyone, would
	// hand the app another session or another person's
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

// DeviceToken answers the app's poll: 202 while waiting, the tokens once approved (one
// time), 410 when its code has expired or this home no longer holds it (a restart): it
// asks for a new one.
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
	now := h.now()
	if now.Unix() > int64(binary.BigEndian.Uint64(raw)) || !hmac.Equal(raw[8:12], h.keys().id[:]) {
		httperror.Write(w, r, gone("code expired: ask for a new one"))
		return
	}
	if h.Minter == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	code := h.deviceCode(raw)
	asked, held := h.requests.get(code, now)
	t, err := h.Store.Consume(r.Context(), deviceTicketPrefix+code)
	if errors.Is(err, discovery.ErrCodeNotFound) {
		t, err = h.Store.Consume(r.Context(), legacyTicketPrefix+code)
	}
	switch {
	case errors.Is(err, discovery.ErrCodeNotFound):
		if !held {
			// nobody can approve a code this home does not hold: a new one, at once
			httperror.Write(w, r, gone("code not held: ask for a new one"))
			return
		}
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "pending", "interval": devicePollInterval})
		return
	case errors.Is(err, discovery.ErrCodeExpired):
		httperror.Write(w, r, gone("approval expired: ask for a new code"))
		return
	case err != nil:
		writePairingError(w, r, err)
		return
	}
	// the session says what signed in: the app, platform and device the request
	// recorded (X-Maktaba-Client), else the poll's own header; a TV from before the
	// header stays "tv"
	who := asked.Client
	if !held || who.App == client.Unknown || who.App == "" {
		who = client.Parse(r.Header.Get(client.Header))
	}
	label := cleanLabel(req.DeviceLabel)
	if label == "" {
		label = asked.Label
	}
	kind := legacyDeviceKind
	if who.App != client.Unknown && who.App != "" {
		kind = who.App
		if label == "" {
			label = describeClient(who)
		}
	}
	tok, err := h.Minter.Mint(r.Context(), t.UserID, kind, label)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("mint device tokens").Wrap(err))
		return
	}
	h.requests.drop(code)
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

// DeviceKeyFrom derives the device pairing key from a home's secret (its signing key),
// so the device codes apps wait on keep their meaning across a restart.
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

// cleanLabel is a device label without control characters, at most maxDeviceLabel
// bytes (cut on a rune boundary).
func cleanLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if len(s) <= maxDeviceLabel {
		return s
	}
	cut := maxDeviceLabel
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}

// describeClient is a session label from X-Maktaba-Client: "player (ios; phone)".
func describeClient(c client.Info) string {
	var parts []string
	for _, p := range []string{c.Platform, c.Device} {
		if p != client.Unknown && p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return c.App
	}
	return c.App + " (" + strings.Join(parts, "; ") + ")"
}

// known is a part of X-Maktaba-Client, "" when the app did not say it.
func known(part string) string {
	if part == client.Unknown {
		return ""
	}
	return part
}

// readOptionalJSON reads a small JSON body into v when there is one (an empty body is
// none): the TVs from before story 42.6 send nothing.
func readOptionalJSON(r *http.Request, v any) *httperror.Error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<10+1))
	if err != nil {
		return httperror.BadRequest("unreadable body")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return common.ReadJSON(r, v, 1<<10)
}

func codeNotHeld() *httperror.Error {
	return httperror.NotFound("code not found or expired: ask the device for a new one")
}

func unauthorized() *httperror.Error {
	return &httperror.Error{Type: "about:blank", Title: "unauthorized", Status: http.StatusUnauthorized,
		Detail: "authentication required"}
}

func gone(detail string) *httperror.Error {
	return &httperror.Error{Type: httperror.TypeConflict, Title: "gone", Status: http.StatusGone, Detail: detail}
}
