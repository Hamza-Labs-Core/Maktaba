// A TV signed in from anywhere with a code through Maktaba Cloud (Epic 58 story 58.03). The TV
// asked the cloud for a code (POST /v1/tv/codes) and shows it; a person signed in on the home
// approves it here:
//
//	GET  /api/pairing/cloud-code/{code}  signed in on the home: what asked with a cloud code
//	                                     (its platform, app version and country), so the person
//	                                     can say "this is my TV"
//	POST /api/pairing/cloud-approve      signed in on the home: approve it {code, as_user?}
//
// Approving makes a device code of this home for the approver (as POST /api/pairing/device
// would for a TV on the LAN), approves it at once, and claims the cloud's code with that device
// code as the grant, through the relay agent (which holds the server's secret). The cloud adds
// the home's connections and hands both to the TV once; the TV redeems the grant on the home
// (POST /api/pairing/device/token), so it is signed in as the approver, with their role,
// libraries and parental policy, like any device approved here. No cloud account is involved:
// the home's registration with the cloud is enough.
package discovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	// cloudCodeAlphabet is the cloud's TV codes' characters (cloud/internal/handlers/tvcodes):
	// story 15.06's pairing alphabet without 0 and 1.
	cloudCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"
	// ViaCloudCode marks a sign-in a TV collected with a cloud code (its session says so).
	ViaCloudCode = "cloud_code"
)

// TypeCloudNotLinked: this home is not registered with Maktaba Cloud (remote access is off), so
// it cannot claim a code from the cloud.
var TypeCloudNotLinked = httperror.Type("cloud-not-linked")

// CloudCode is what the cloud says about a TV's code.
type CloudCode struct {
	Code       string    `json:"code"`
	Platform   string    `json:"platform"`
	AppVersion string    `json:"app_version"`
	Country    string    `json:"country"`
	ExpiresAt  time.Time `json:"expires_at"`
}

var (
	// ErrCloudNotLinked: no relay agent, or the cloud does not know this home.
	ErrCloudNotLinked = errors.New("discovery: this home is not registered with Maktaba Cloud")
	// ErrCloudCodeNotFound: the cloud holds no open code by that name.
	ErrCloudCodeNotFound = errors.New("discovery: no open cloud code")
	// ErrCloudCodeClaimed: someone approved the code first.
	ErrCloudCodeClaimed = errors.New("discovery: cloud code already approved")
)

// CloudCodes reaches the cloud's TV codes as this home.
type CloudCodes interface {
	Lookup(ctx context.Context, code string) (CloudCode, error)
	Claim(ctx context.Context, code, grant string) (CloudCode, error)
}

// AgentCloudCodes is CloudCodes through the relay agent's loopback admin port
// (/admin/cloud-link/tv-codes/, cloud/internal/cloudlink).
type AgentCloudCodes struct {
	// Addr is the agent's host:port; "" reads MAKTABA_CLOUDLINK_ADMIN (none: not linked).
	Addr   string
	Client *http.Client
}

func (a AgentCloudCodes) addr() string {
	if a.Addr != "" {
		return a.Addr
	}
	return os.Getenv("MAKTABA_CLOUDLINK_ADMIN")
}

// Lookup asks the cloud what asked with code.
func (a AgentCloudCodes) Lookup(ctx context.Context, code string) (CloudCode, error) {
	return a.call(ctx, http.MethodGet, code, nil)
}

// Claim gives code to this home with grant.
func (a AgentCloudCodes) Claim(ctx context.Context, code, grant string) (CloudCode, error) {
	b, _ := json.Marshal(map[string]string{"grant": grant})
	return a.call(ctx, http.MethodPost, code+"/claim", b)
}

func (a AgentCloudCodes) call(ctx context.Context, method, sub string, body []byte) (CloudCode, error) {
	addr := a.addr()
	if addr == "" {
		return CloudCode{}, ErrCloudNotLinked
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+"/admin/cloud-link/tv-codes/"+sub, rd)
	if err != nil {
		return CloudCode{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := a.Client
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		// no agent listening: remote access is off on this home
		if errors.Is(err, syscall.ECONNREFUSED) {
			return CloudCode{}, ErrCloudNotLinked
		}
		return CloudCode{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		var c CloudCode
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&c); err != nil {
			return CloudCode{}, err
		}
		return c, nil
	case http.StatusNotFound:
		return CloudCode{}, ErrCloudCodeNotFound
	case http.StatusConflict:
		return CloudCode{}, ErrCloudCodeClaimed
	case http.StatusUnauthorized:
		// the cloud does not know this home's secret: its registration is gone
		return CloudCode{}, ErrCloudNotLinked
	default:
		return CloudCode{}, errors.New("the cloud answered " + resp.Status)
	}
}

// MountCloudCode attaches the cloud-code routes.
func (h *Handler) MountCloudCode(r chi.Router) {
	r.Get("/api/pairing/cloud-code/{code}", h.CloudCodeLookup)
	r.Post("/api/pairing/cloud-approve", h.CloudApprove)
}

// validCloudCode is whether code (normalized) could be a cloud TV code.
func validCloudCode(code string) bool {
	if len(code) != 8 {
		return false
	}
	for _, c := range code {
		if !strings.ContainsRune(cloudCodeAlphabet, c) {
			return false
		}
	}
	return true
}

// cloudCaller is the signed-in person who may approve a cloud code: a sign-in on the home
// (a cloud sign-in cannot approve a device, as for Approve).
func cloudCaller(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return nil
	}
	if p.ViaCloud() {
		httperror.Write(w, r, httperror.Forbidden(httperror.TypeCloudSession,
			"a sign-in through Maktaba Cloud cannot sign in a device; sign in on the home"))
		return nil
	}
	return p
}

func cloudCodeError(err error) *httperror.Error {
	switch {
	case errors.Is(err, ErrCloudNotLinked):
		return (&httperror.Error{Type: TypeCloudNotLinked, Title: "not registered with Maktaba Cloud",
			Status: http.StatusConflict,
			Detail: "a code from Maktaba Cloud needs remote access turned on for this home; use the home's own code"}).
			With("code", "cloud_not_linked")
	case errors.Is(err, ErrCloudCodeNotFound):
		return codeNotHeld()
	case errors.Is(err, ErrCloudCodeClaimed):
		return httperror.Conflict("", "code already approved")
	default:
		return httperror.BadGateway("Maktaba Cloud is out of reach").Wrap(err)
	}
}

// CloudCodeLookup says what asked with a cloud code: the TV's platform, app version and its
// address's country, for "Is this your TV?".
func (h *Handler) CloudCodeLookup(w http.ResponseWriter, r *http.Request) {
	if cloudCaller(w, r) == nil {
		return
	}
	code := discovery.NormalizeCode(chi.URLParam(r, "code"))
	if !validCloudCode(code) {
		httperror.Write(w, r, codeNotHeld())
		return
	}
	if h.CloudCodes == nil {
		httperror.Write(w, r, cloudCodeError(ErrCloudNotLinked))
		return
	}
	c, err := h.CloudCodes.Lookup(r.Context(), code)
	if err != nil {
		httperror.Write(w, r, cloudCodeError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, c)
}

// CloudApprove signs the TV showing a cloud code in as the caller (or a restricted account
// they look after, as_user): a device code of this home approved at once, handed to the cloud
// as the TV's grant.
func (h *Handler) CloudApprove(w http.ResponseWriter, r *http.Request) {
	p := cloudCaller(w, r)
	if p == nil {
		return
	}
	var req approveRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	code := discovery.NormalizeCode(req.Code)
	if !validCloudCode(code) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "code", Message: "eight letters and digits, as the TV shows"},
		}))
		return
	}
	userID, e := h.approver(r, p, req.AsUser)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.CloudCodes == nil {
		httperror.Write(w, r, cloudCodeError(ErrCloudNotLinked))
		return
	}
	if h.Minter == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	// what asked: shown on the TV's session, and checked before anything is made
	asked, err := h.CloudCodes.Lookup(r.Context(), code)
	if err != nil {
		httperror.Write(w, r, cloudCodeError(err))
		return
	}

	// a device code of this home, as DeviceStart makes for a TV on the LAN
	now := h.now()
	exp := now.Add(DeviceCodeTTL)
	raw := make([]byte, deviceCodeLen)
	binary.BigEndian.PutUint64(raw, uint64(exp.Unix()))
	copy(raw[8:12], h.keys().id[:])
	if _, err := rand.Read(raw[12:]); err != nil {
		httperror.Write(w, r, httperror.Internal("device code").Wrap(err))
		return
	}
	homeCode := h.deviceCode(raw)
	id := make([]byte, 9)
	if _, err := rand.Read(id); err != nil {
		httperror.Write(w, r, httperror.Internal("device request id").Wrap(err))
		return
	}
	platform := cleanLabel(asked.Platform)
	if !h.requests.put(homeCode, deviceRequest{
		ID:       base64.RawURLEncoding.EncodeToString(id),
		Client:   client.Info{App: legacyDeviceKind, Platform: platform, Device: legacyDeviceKind},
		Label:    platform,
		Created:  time.Unix(now.Unix(), 0).UTC(),
		Expires:  time.Unix(exp.Unix(), 0).UTC(),
		LookedUp: now,
		Via:      ViaCloudCode,
		approved: true,
	}, now) {
		httperror.Write(w, r, httperror.Unavailable(30))
		return
	}
	key := deviceTicketPrefix + homeCode
	if err := h.Store.Insert(r.Context(), discovery.PairingTicket{
		Code: key, UserID: userID, IssuedAt: now, ExpiresAt: exp,
	}); err != nil {
		h.requests.drop(homeCode)
		httperror.Write(w, r, httperror.Internal("store approval").Wrap(err))
		return
	}
	grant := base64.RawURLEncoding.EncodeToString(raw)
	claimed, err := h.CloudCodes.Claim(r.Context(), code, grant)
	if err != nil {
		// the grant never reached a TV: take the approval back
		_, _ = h.Store.Consume(r.Context(), key)
		h.requests.drop(homeCode)
		httperror.Write(w, r, cloudCodeError(err))
		return
	}
	if h.Audit != nil {
		h.Audit.WritePairEvent(r.Context(), false, p.UserID, key)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"status": "approved", "user_id": userID,
		"platform": claimed.Platform, "country": claimed.Country,
	})
}
