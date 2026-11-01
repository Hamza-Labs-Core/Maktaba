package discovery

// Epic 42 story 42.6: sign-in by code for every app — what asks is recorded, the
// approver sees it before approving, the device response carries verify_url, and the
// approved app gets the approver's own account and nothing else.

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
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

func startWith(t *testing.T, r http.Handler, clientHeader, body string) deviceStartResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/device", strings.NewReader(body))
	req.Host = "studio.local:8088"
	if clientHeader != "" {
		req.Header.Set("X-Maktaba-Client", clientHeader)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var s deviceStartResponse
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func lookup(r http.Handler, code, user string) *httptest.ResponseRecorder {
	return do(r, http.MethodGet, "/api/pairing/device/"+code, "", user)
}

// The app says what it is (X-Maktaba-Client) and its label; the approver reads both,
// with the code and its expiry, before approving.
func TestDeviceLookupShowsWhatIsAsking(t *testing.T) {
	_, _, r, now := newDeviceHandler(t)
	s := startWith(t, r, "player/1.2.0 (ios; phone)", `{"device_label":"Sara's iPhone"}`)
	rec := lookup(r, strings.ToLower(s.Code), "viewer-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup = %d %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	want := map[string]any{
		"code": s.Code, "client_app": "player", "platform": "ios", "device": "phone",
		"device_label": "Sara's iPhone", "expires_at": now.Add(DeviceCodeTTL).Format(time.RFC3339),
	}
	if len(got) != len(want) {
		t.Fatalf("lookup keys = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("lookup %s = %v, want %v", k, got[k], v)
		}
	}
	// the dashless form is the same code
	if rec := lookup(r, strings.ReplaceAll(s.Code, "-", ""), "viewer-2"); rec.Code != http.StatusOK {
		t.Fatalf("dashless lookup = %d", rec.Code)
	}
}

// An app that says nothing (an old TV: no header, no body) reads as empty strings.
func TestDeviceLookupOfAnOldTV(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	rec := lookup(r, s.Code, "viewer-1")
	var got deviceLookupResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.ClientApp != "" || got.Platform != "" || got.Device != "" || got.DeviceLabel != "" {
		t.Fatalf("lookup = %d %+v", rec.Code, got)
	}
}

// Role rules: anonymous gets 401 on the lookup and the approve; an unknown, malformed
// or expired code is 404 (nothing to approve).
func TestDeviceLookupRules(t *testing.T) {
	_, _, r, now := newDeviceHandler(t)
	s := startDevice(t, r)
	if rec := lookup(r, s.Code, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous lookup = %d, want 401", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous approve = %d, want 401", rec.Code)
	}
	for _, c := range []string{"ZZZZ-ZZZZ", "nope", "ABCD-123O"} {
		if c == s.Code {
			continue
		}
		if rec := lookup(r, c, "viewer-1"); rec.Code != http.StatusNotFound {
			t.Fatalf("lookup of %s = %d, want 404", c, rec.Code)
		}
	}
	*now = now.Add(DeviceCodeTTL + time.Second)
	if rec := lookup(r, s.Code, "viewer-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("lookup of an expired code = %d, want 404", rec.Code)
	}
}

// A code this home never issued cannot be approved (404): approving it would sign in
// nothing, or something the approver never saw.
func TestDeviceApproveOfAnUnknownCode(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	other := "ZZZZ-ZZZZ"
	if other == s.Code {
		other = "YYYY-YYYY"
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+other+`"}`, "viewer-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("approve of an unknown code = %d, want 404", rec.Code)
	}
	if fm.calls != 0 {
		t.Fatal("minted")
	}
}

// The approved app gets the approver's own account (and so their role): a viewer cannot
// name anyone else, and an app is signed in as the viewer, not as an admin.
func TestDeviceApprovalIsTheApproversOwn(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startWith(t, r, "remote/1.0.0 (android; tablet)", `{"device_label":"Tab"}`)
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`","user_id":"admin-1"}`, "viewer-1"); rec.Code < 400 {
		t.Fatalf("approve naming another user = %d, want a refusal", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d", rec.Code)
	}
	rec := do(r, http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+s.DeviceCode+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("collect = %d %s", rec.Code, rec.Body.String())
	}
	// the app's own kind and its label from the start (the poll gave none)
	if fm.lastUID != "viewer-1" || fm.lastKnd != "remote" || fm.lastLbl != "Tab" {
		t.Fatalf("minted for %q %q %q", fm.lastUID, fm.lastKnd, fm.lastLbl)
	}
	// collected: the code is no longer held
	if rec := lookup(r, s.Code, "viewer-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("lookup after collection = %d, want 404", rec.Code)
	}
}

// A restart loses what asked: the waiting app gets 410 at once and asks for a new code;
// the approver gets 404 (a code they cannot see is not approved).
func TestDeviceCodeNotHeldAfterARestart(t *testing.T) {
	key := DeviceKeyFrom([]byte("the home's signing key"))
	store := discovery.NewMemoryPairingStore()
	mk := func() http.Handler {
		h := &Handler{Store: store, Minter: &fakeMinter{}, TTL: time.Minute, DeviceKey: key}
		r := chi.NewRouter()
		h.Mount(r)
		h.MountDevice(r, -1)
		return r
	}
	s := startDevice(t, mk())
	after := mk()
	if rec := poll(after, s.DeviceCode); rec.Code != http.StatusGone {
		t.Fatalf("poll of a code not held = %d, want 410", rec.Code)
	}
	if rec := do(after, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("approve of a code not held = %d, want 404", rec.Code)
	}
}

// A TV approved before story 42.6 ("tv." ticket) still collects its tokens.
func TestDeviceLegacyApprovalIsCollected(t *testing.T) {
	h, fm, r, now := newDeviceHandler(t)
	s := startDevice(t, r)
	code := discovery.NormalizeCode(s.Code)
	if err := h.Store.Insert(context.Background(), discovery.PairingTicket{
		Code: "tv." + code, UserID: "viewer-1", IssuedAt: *now, ExpiresAt: now.Add(DeviceCodeTTL),
	}); err != nil {
		t.Fatal(err)
	}
	if rec := poll(r, s.DeviceCode); rec.Code != http.StatusOK || fm.lastUID != "viewer-1" || fm.lastKnd != "tv" {
		t.Fatalf("legacy collect = %d (%q %q)", rec.Code, fm.lastUID, fm.lastKnd)
	}
}

// verify_url is the home's first address + verify_path; with no address of its own the
// home answers with the one the app reached it at.
func TestDeviceStartVerifyURL(t *testing.T) {
	h, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	if s.VerifyURL != "http://example.com"+s.VerifyPath {
		t.Fatalf("verify_url without a home = %q", s.VerifyURL)
	}
	h.Home = func(context.Context, *http.Request) HomeInfo {
		return HomeInfo{URLs: []string{"http://192.168.1.5:8088/", "https://kitchen.relay.maktaba.tv"}}
	}
	s = startDevice(t, r)
	if s.VerifyURL != "http://192.168.1.5:8088/pair?code="+s.Code || s.VerifyPath != "/pair?code="+s.Code {
		t.Fatalf("verify_url = %q path %q", s.VerifyURL, s.VerifyPath)
	}
}

// The start's body is optional, small and strict; the label is trimmed and bounded.
func TestDeviceStartBody(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	for _, body := range []string{`{"device_label":1}`, `{"user_id":"x"}`, `not json`} {
		if rec := do(r, http.MethodPost, "/api/pairing/device", body, ""); rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("start with %s = %d, want a 4xx", body, rec.Code)
		}
	}
	if rec := do(r, http.MethodPost, "/api/pairing/device", `{"device_label":"`+strings.Repeat("x", 2000)+`"}`, ""); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("an oversized body = %d, want a 4xx", rec.Code)
	}
	s := startWith(t, r, "", `{"device_label":"  `+strings.Repeat("é", 60)+`\u0007  "}`)
	var got deviceLookupResponse
	_ = json.Unmarshal(lookup(r, s.Code, "viewer-1").Body.Bytes(), &got)
	if len(got.DeviceLabel) > maxDeviceLabel || !strings.HasPrefix(got.DeviceLabel, "é") || strings.ContainsRune(got.DeviceLabel, '\x07') {
		t.Fatalf("label = %q (%d bytes)", got.DeviceLabel, len(got.DeviceLabel))
	}
	if strings.ContainsRune(got.DeviceLabel, 0xFFFD) {
		t.Fatal("the label was cut inside a character")
	}
}

// The start keeps its per-IP limit (a public route): past it, 429.
func TestDeviceStartIsRateLimited(t *testing.T) {
	h := &Handler{Store: discovery.NewMemoryPairingStore(), Minter: &fakeMinter{}}
	r := chi.NewRouter()
	h.MountDevice(r, 3)
	codes := []int{}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/pairing/device", nil)
		req.RemoteAddr = "192.0.2.7:5555"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != http.StatusCreated || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v", codes)
	}
}

// Too many codes waiting at once: the home asks the app to try again later.
func TestDeviceRequestsAreBounded(t *testing.T) {
	var d deviceRequests
	now := time.Now()
	for i := 0; i < maxDeviceRequests; i++ {
		if !d.put(string(rune(i))+"x", deviceRequest{Expires: now.Add(time.Minute)}, now) {
			t.Fatalf("put %d refused", i)
		}
	}
	if d.put("one-more", deviceRequest{Expires: now.Add(time.Minute)}, now) {
		t.Fatal("past the bound")
	}
	// once they expire, room again
	if !d.put("later", deviceRequest{Expires: now.Add(2 * time.Hour)}, now.Add(time.Hour)) {
		t.Fatal("expired requests were not dropped")
	}
}

// The apps tell "an older home without the route" (a plain 404) from "an unknown code"
// by the problem body: an unknown code always answers application/problem+json.
func TestDeviceLookupUnknownIsAProblem(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	for _, c := range []string{"ZZZZ-ZZZZ", "zzzzzzzz", "nope"} {
		rec := lookup(r, c, "viewer-1")
		var body map[string]any
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "problem+json") ||
			json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["status"] != float64(404) {
			t.Fatalf("lookup %s = %d %q %s", c, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
}

// Codes are normalised everywhere: the path without the dash and in lower case, the
// approve body with the dash or without.
func TestDeviceCodesAreNormalised(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	bare := strings.ToLower(strings.ReplaceAll(s.Code, "-", ""))
	if rec := lookup(r, bare, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("lookup %s = %d", bare, rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":" `+bare+` "}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve %s = %d %s", bare, rec.Code, rec.Body.String())
	}
}

// The session is labelled from X-Maktaba-Client (app, platform, device), never "tv"
// for a phone; the poll's own header counts when the start sent none.
func TestDeviceSessionIsLabelledFromTheClient(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startWith(t, r, "player/1.2.0 (android; foldable)", "")
	do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1")
	do(r, http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+s.DeviceCode+`"}`, "")
	if fm.lastKnd != "player" || fm.lastLbl != "player (android; foldable)" {
		t.Fatalf("minted %q %q", fm.lastKnd, fm.lastLbl)
	}

	s = startDevice(t, r) // no header at the start
	do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`, "viewer-1")
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/device/token", strings.NewReader(`{"device_code":"`+s.DeviceCode+`"}`))
	req.Header.Set("X-Maktaba-Client", "remote/1.0 (macos; desktop)")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if fm.lastKnd != "remote" || fm.lastLbl != "remote (macos; desktop)" {
		t.Fatalf("minted %q %q", fm.lastKnd, fm.lastLbl)
	}
}

// A sign-in through Maktaba Cloud may look a code up but not approve it.
func TestDeviceLookupFromACloudSession(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	req := httptest.NewRequest(http.MethodGet, "/api/pairing/device/"+s.Code, nil)
	req = req.WithContext(principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "viewer-1", CloudScope: "viewer"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cloud lookup = %d", rec.Code)
	}
}
