package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type linkRig struct {
	d   *DeviceLink
	m   *fakeMinter
	r   http.Handler
	now time.Time
}

func newLinkRig(t *testing.T) *linkRig {
	t.Helper()
	rig := &linkRig{m: &fakeMinter{}, now: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)}
	rig.d = &DeviceLink{Minter: rig.m, NowFunc: func() time.Time { return rig.now }}
	r := chi.NewRouter()
	rig.d.Mount(r)
	rig.r = r
	return rig
}

// do sends body as JSON; user "" sends no principal.
func (rig *linkRig) do(t *testing.T, method, path, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Host = "192.168.50.177:8088"
	if user != "" {
		req = req.WithContext(principal.WithPrincipal(context.Background(), &principal.Principal{UserID: user}))
	}
	rec := httptest.NewRecorder()
	rig.r.ServeHTTP(rec, req)
	return rec
}

func (rig *linkRig) start(t *testing.T) startResponse {
	t.Helper()
	rec := rig.do(t, http.MethodPost, "/api/auth/device/start", "", map[string]string{"device_kind": "tv", "device_label": "Living room TV"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var s startResponse
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeviceLinkStartNamesTheApprovePage(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	if len(s.UserCode) != 9 || s.UserCode[4] != '-' {
		t.Fatalf("user code %q, want XXXX-XXXX", s.UserCode)
	}
	if len(s.DeviceCode) < 40 {
		t.Fatalf("device code too short: %q", s.DeviceCode)
	}
	if s.VerificationURI != "http://192.168.50.177:8088/link" {
		t.Fatalf("verification uri %q", s.VerificationURI)
	}
	if s.VerificationURIComplete != s.VerificationURI+"?code="+s.UserCode {
		t.Fatalf("complete uri %q", s.VerificationURIComplete)
	}
	if s.Interval != DeviceLinkInterval || s.ExpiresIn != int(DeviceLinkTTL.Seconds()) {
		t.Fatalf("interval/expiry %d/%d", s.Interval, s.ExpiresIn)
	}
}

func TestDeviceLinkPendingUntilApproved(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": s.DeviceCode})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("poll before approval status=%d", rec.Code)
	}
	if rig.m.calls != 0 {
		t.Fatal("minted before anyone approved")
	}
}

// The TV is signed in as the person who approved it, with the device it named.
func TestDeviceLinkApprovedSignsInTheApprover(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/approve", "viewer-1", map[string]string{"code": strings.ToLower(s.UserCode)}); rec.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": s.DeviceCode})
	if rec.Code != http.StatusOK {
		t.Fatalf("poll after approval status=%d body=%s", rec.Code, rec.Body.String())
	}
	var tok exchangeResponse
	if err := json.NewDecoder(rec.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	if tok.UserID != "viewer-1" || tok.AccessToken != "access.viewer-1" || rig.m.lastUID != "viewer-1" {
		t.Fatalf("tokens for %q (minted for %q), want viewer-1", tok.UserID, rig.m.lastUID)
	}
	if rig.m.lastKnd != "tv" || rig.m.lastLbl != "Living room TV" {
		t.Fatalf("device %q/%q", rig.m.lastKnd, rig.m.lastLbl)
	}
	// spent: the next poll (or a replay) gets nothing
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": s.DeviceCode}); rec.Code != http.StatusGone {
		t.Fatalf("second poll status=%d, want 410", rec.Code)
	}
	if rig.m.calls != 1 {
		t.Fatalf("minted %d times", rig.m.calls)
	}
}

// Approving and looking up need a signed-in person; the API enforces it.
func TestDeviceLinkApproveAndLookupRefuseAnonymous(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/approve", "", map[string]string{"code": s.UserCode}); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous approve status=%d", rec.Code)
	}
	if rec := rig.do(t, http.MethodGet, "/api/auth/device?code="+s.UserCode, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous lookup status=%d", rec.Code)
	}
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": s.DeviceCode}); rec.Code != http.StatusAccepted {
		t.Fatalf("still pending after refused approvals: %d", rec.Code)
	}
}

// The code on the screen names the TV; it is not the secret that takes the tokens.
func TestDeviceLinkCodeAloneTakesNothing(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	rig.do(t, http.MethodPost, "/api/auth/device/approve", "viewer-1", map[string]string{"code": s.UserCode})
	for _, guess := range []string{s.UserCode, strings.ReplaceAll(s.UserCode, "-", "")} {
		if rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": guess}); rec.Code != http.StatusGone {
			t.Fatalf("poll with the user code status=%d", rec.Code)
		}
	}
	if rig.m.calls != 0 {
		t.Fatal("minted for the user code")
	}
}

// Once one person approved, another cannot take the TV over.
func TestDeviceLinkSecondApproverRefused(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	rig.do(t, http.MethodPost, "/api/auth/device/approve", "viewer-1", map[string]string{"code": s.UserCode})
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/approve", "admin-1", map[string]string{"code": s.UserCode}); rec.Code != http.StatusConflict {
		t.Fatalf("second approver status=%d", rec.Code)
	}
	rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": s.DeviceCode})
	var tok exchangeResponse
	_ = json.NewDecoder(rec.Body).Decode(&tok)
	if tok.UserID != "viewer-1" {
		t.Fatalf("tokens for %q, want the first approver", tok.UserID)
	}
}

func TestDeviceLinkLookupShowsTheDevice(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	rec := rig.do(t, http.MethodGet, "/api/auth/device?code="+strings.ReplaceAll(s.UserCode, "-", ""), "viewer-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup status=%d", rec.Code)
	}
	var l lookupResponse
	_ = json.NewDecoder(rec.Body).Decode(&l)
	if l.Code != s.UserCode || l.DeviceLabel != "Living room TV" || l.DeviceKind != "tv" {
		t.Fatalf("lookup %+v", l)
	}
	if rec := rig.do(t, http.MethodGet, "/api/auth/device?code=ZZZZ-ZZZZ", "viewer-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown code status=%d", rec.Code)
	}
}

func TestDeviceLinkExpires(t *testing.T) {
	rig := newLinkRig(t)
	s := rig.start(t)
	rig.now = rig.now.Add(DeviceLinkTTL + time.Second)
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/approve", "viewer-1", map[string]string{"code": s.UserCode}); rec.Code != http.StatusNotFound {
		t.Fatalf("approve after expiry status=%d", rec.Code)
	}
	if rec := rig.do(t, http.MethodPost, "/api/auth/device/token", "", map[string]string{"device_code": s.DeviceCode}); rec.Code != http.StatusGone {
		t.Fatalf("poll after expiry status=%d", rec.Code)
	}
	if len(rig.d.byCode) != 0 || len(rig.d.bySecret) != 0 {
		t.Fatal("expired ticket kept")
	}
}

func TestDeviceLinkUnavailableWithoutMinter(t *testing.T) {
	d := &DeviceLink{}
	r := chi.NewRouter()
	d.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/device/start", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}
