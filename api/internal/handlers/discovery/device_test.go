package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

func newDeviceHandler(t *testing.T) (*Handler, *fakeMinter, http.Handler, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	store := discovery.NewMemoryPairingStore()
	store.SetNow(func() time.Time { return now })
	fm := &fakeMinter{}
	h := &Handler{Store: store, Minter: fm, TTL: time.Minute, NowFunc: func() time.Time { return now }}
	r := chi.NewRouter()
	h.Mount(r)
	h.MountDevice(r, -1)
	return h, fm, r, &now
}

func do(r http.Handler, method, path, body string, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req = req.WithContext(principal.WithPrincipal(context.Background(), &principal.Principal{UserID: user}))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func startDevice(t *testing.T, r http.Handler) deviceStartResponse {
	t.Helper()
	rec := do(r, http.MethodPost, "/api/pairing/device", "", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var s deviceStartResponse
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func poll(r http.Handler, deviceCode string) *httptest.ResponseRecorder {
	return do(r, http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+deviceCode+`","device_label":"Living room"}`, "")
}

// The TV shows a code, waits, and is signed in as the person who approved it: once.
func TestDeviceFlowSignsTheTVInAsTheApprover(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	if len(s.Code) != 9 || s.Code[4] != '-' || s.DeviceCode == "" || s.VerifyPath != "/pair?code="+s.Code {
		t.Fatalf("start = %+v", s)
	}
	if rec := poll(r, s.DeviceCode); rec.Code != http.StatusAccepted {
		t.Fatalf("poll before approval = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+strings.ToLower(s.Code)+`"}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body.String())
	}
	rec := poll(r, s.DeviceCode)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll after approval = %d %s", rec.Code, rec.Body.String())
	}
	var tok exchangeResponse
	_ = json.NewDecoder(rec.Body).Decode(&tok)
	if tok.UserID != "viewer-1" || tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("tokens = %+v", tok)
	}
	if fm.lastUID != "viewer-1" || fm.lastKnd != "tv" || fm.lastLbl != "Living room" {
		t.Fatalf("minted for %q %q %q", fm.lastUID, fm.lastKnd, fm.lastLbl)
	}
	if rec := poll(r, s.DeviceCode); rec.Code != http.StatusConflict || fm.calls != 1 {
		t.Fatalf("second collect = %d, mints %d: the approval is one session", rec.Code, fm.calls)
	}
}

// Only the TV holding the device code collects: another device code (another TV, or one
// made up) never reaches the approval, and the shown code is not a device code.
func TestDeviceApprovalIsTheRequestingTVsOnly(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	other := startDevice(t, r)
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d", rec.Code)
	}
	if rec := poll(r, other.DeviceCode); rec.Code != http.StatusAccepted {
		t.Fatalf("another TV's poll = %d, want 202", rec.Code)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(s.DeviceCode)
	raw[len(raw)-1] ^= 1
	// an altered device code shows another code, one nobody asked for: 410, ask anew
	if rec := poll(r, base64.RawURLEncoding.EncodeToString(raw)); rec.Code != http.StatusGone {
		t.Fatalf("an altered device code = %d, want 410", rec.Code)
	}
	if rec := poll(r, strings.ReplaceAll(s.Code, "-", "")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the shown code as a device code = %d, want 422", rec.Code)
	}
	if fm.calls != 0 {
		t.Fatalf("minted %d times", fm.calls)
	}
	// and the phone pairing exchange never reaches a TV approval
	if rec := do(r, http.MethodPost, "/api/pairing/exchange", `{"code":"`+s.Code+`"}`, "intruder"); rec.Code != http.StatusNotFound {
		t.Fatalf("exchange of a TV code = %d, want 404", rec.Code)
	}
}

// Approving needs a signed-in person, a well-formed code, and stands once.
func TestDeviceApproveRules(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous approve = %d, want 401", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"ILOU-1234"}`, "viewer-1"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("approve of a malformed code = %d, want 422", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "admin-1"); rec.Code != http.StatusConflict {
		t.Fatalf("second approve = %d, want 409 (it would replace the approver)", rec.Code)
	}
}

// An expired code is gone: the TV asks for a new one.
func TestDeviceCodeExpires(t *testing.T) {
	_, _, r, now := newDeviceHandler(t)
	s := startDevice(t, r)
	*now = now.Add(DeviceCodeTTL + time.Second)
	if rec := poll(r, s.DeviceCode); rec.Code != http.StatusGone {
		t.Fatalf("poll of an expired code = %d, want 410", rec.Code)
	}
}

// Another process (a restart) has another key: the old device code no longer matches.
func TestDeviceCodeIsBoundToTheKey(t *testing.T) {
	a := &Handler{DeviceKey: []byte("one")}
	b := &Handler{DeviceKey: []byte("two")}
	raw := make([]byte, 24)
	if a.deviceCode(raw) == b.deviceCode(raw) {
		t.Fatal("two keys made the same code")
	}
	if !validDeviceCode(a.deviceCode(raw)) {
		t.Fatalf("code %q is not in the alphabet", a.deviceCode(raw))
	}
}

// A restart with the same key (the home derives it from its signing key) keeps the
// waiting TV's code; another key answers 410 at once, so the TV asks for a new code.
func TestDeviceCodeOutlivesARestartWithItsKey(t *testing.T) {
	key := DeviceKeyFrom([]byte("the home's signing key"))
	store := discovery.NewMemoryPairingStore()
	mk := func(k []byte) http.Handler {
		h := &Handler{Store: store, Minter: &fakeMinter{}, TTL: time.Minute, DeviceKey: k}
		r := chi.NewRouter()
		h.Mount(r)
		h.MountDevice(r, -1)
		return r
	}
	before := mk(key)
	s := startDevice(t, before)
	if rec := do(before, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d", rec.Code)
	}
	if rec := poll(mk(DeviceKeyFrom([]byte("a new signing key"))), s.DeviceCode); rec.Code != http.StatusGone {
		t.Fatalf("another key's poll = %d, want 410", rec.Code)
	}
	if rec := poll(mk(key), s.DeviceCode); rec.Code != http.StatusOK {
		t.Fatalf("after a restart with the same key = %d, want 200", rec.Code)
	}
}

// Two people approving one code at once: one approval stands, the other is refused (the
// TV is never signed in as the second, nor twice).
func TestDeviceApprovalsRaceToOne(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	codes := make(chan int, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes <- do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, fmt.Sprintf("user-%d", i)).Code
		}(i)
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("approve = %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d approvals stood, want 1", ok)
	}
}

// A sign-in through Maktaba Cloud cannot sign in a TV: the TV's tokens would outlive the
// cloud link with the person's full role (Epic 42 story 42.7, as for a phone's pairing).
func TestDeviceApproveRefusesACloudSession(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/approve", strings.NewReader(`{"code":"`+s.Code+`"}`))
	req = req.WithContext(principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "viewer-1", CloudScope: "viewer"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("approve from a cloud session = %d, want 403", rec.Code)
	}
	if rec := poll(r, s.DeviceCode); rec.Code != http.StatusAccepted || fm.calls != 0 {
		t.Fatalf("the TV was signed in (%d, %d mints)", rec.Code, fm.calls)
	}
}
