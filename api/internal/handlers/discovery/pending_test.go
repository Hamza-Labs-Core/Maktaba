package discovery

// GET /api/pairing/pending: a signed-in app can say "a TV is asking to sign in" without
// ever learning the code. The person still types or scans it from the TV.

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

func pending(t *testing.T, r http.Handler, p *principal.Principal) (int, pendingResponse, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pairing/pending", nil)
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(context.Background(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out pendingResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("pending body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out, rec.Body.String()
}

// What is waiting, newest first: the app, platform, device and label, and when it asked
// and runs out; gone once collected or expired.
func TestDevicePendingListsWhatIsWaiting(t *testing.T) {
	_, _, r, now := newDeviceHandler(t)
	viewer := &principal.Principal{UserID: "viewer-1"}
	if code, out, body := pending(t, r, viewer); code != http.StatusOK || out.Count != 0 || out.Requests == nil {
		t.Fatalf("nothing waiting = %d %s", code, body)
	}
	tv := startWith(t, r, "tv/0.4.0 (android; tv)", `{"device_label":"Living room TV"}`)
	*now = now.Add(time.Minute)
	startWith(t, r, "player/1.2.0 (ios; phone)", `{"device_label":"Sara's iPhone"}`)

	code, out, _ := pending(t, r, viewer)
	if code != http.StatusOK || out.Count != 2 || len(out.Requests) != 2 {
		t.Fatalf("pending = %d %+v", code, out)
	}
	first, second := out.Requests[0], out.Requests[1]
	if first.DeviceLabel != "Sara's iPhone" || second.DeviceLabel != "Living room TV" {
		t.Fatalf("not newest first: %+v", out.Requests)
	}
	if second.ClientApp != "tv" || second.Platform != "android" || second.Device != "tv" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("tv request = %+v", second)
	}
	if !second.CreatedAt.Equal(now.Add(-time.Minute)) || !second.ExpiresAt.Equal(now.Add(-time.Minute).Add(DeviceCodeTTL)) {
		t.Fatalf("tv times = %v %v", second.CreatedAt, second.ExpiresAt)
	}

	// approved and collected: no longer waiting
	do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+tv.Code+`"}`, "viewer-1")
	if rec := poll(r, tv.DeviceCode); rec.Code != http.StatusOK {
		t.Fatalf("collect = %d %s", rec.Code, rec.Body.String())
	}
	if _, out, _ := pending(t, r, viewer); out.Count != 1 || out.Requests[0].DeviceLabel != "Sara's iPhone" {
		t.Fatalf("after collecting = %+v", out)
	}
	// expired: gone
	*now = now.Add(DeviceCodeTTL + time.Second)
	if _, out, _ := pending(t, r, viewer); out.Count != 0 {
		t.Fatalf("after expiry = %+v", out)
	}
}

// Who may ask: anonymous 401; a sign-in on the home, of any role, sees the list; a
// sign-in through Maktaba Cloud (which cannot approve) sees an empty one.
func TestDevicePendingRoles(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	startDevice(t, r)
	if code, _, _ := pending(t, r, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", code)
	}
	for _, p := range []*principal.Principal{{UserID: "viewer-1"}, {UserID: "admin-1", IsAdmin: true}} {
		if code, out, _ := pending(t, r, p); code != http.StatusOK || out.Count != 1 {
			t.Fatalf("%s on the home = %d %+v", p.UserID, code, out)
		}
	}
	for _, scope := range []string{principal.CloudScopePlay, principal.CloudScopeManage} {
		code, out, body := pending(t, r, &principal.Principal{UserID: "viewer-1", CloudScope: scope})
		if code != http.StatusOK || out.Count != 0 || len(out.Requests) != 0 || !strings.Contains(body, `"requests":[]`) {
			t.Fatalf("cloud %s = %d %s, want an empty list", scope, code, body)
		}
	}
}

// The list never carries a code or a device code, in any form: it cannot be turned into
// an approval or a sign-in.
func TestDevicePendingNeverCarriesTheCode(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	starts := []deviceStartResponse{
		startDevice(t, r),
		startWith(t, r, "tv/0.4.0 (android; tv)", `{"device_label":"Living room TV"}`),
	}
	_, out, body := pending(t, r, &principal.Principal{UserID: "viewer-1"})
	if out.Count != 2 {
		t.Fatalf("pending = %s", body)
	}
	for _, s := range starts {
		for _, secret := range []string{s.Code, strings.ReplaceAll(s.Code, "-", ""), s.DeviceCode, s.VerifyPath} {
			if strings.Contains(body, secret) || strings.Contains(strings.ToUpper(body), strings.ToUpper(secret)) {
				t.Fatalf("the list carries %q: %s", secret, body)
			}
		}
	}
	var raw struct {
		Requests []map[string]any `json:"requests"`
	}
	_ = json.Unmarshal([]byte(body), &raw)
	allowed := map[string]bool{"id": true, "client_app": true, "platform": true, "device": true, "device_label": true, "created_at": true, "expires_at": true}
	for _, req := range raw.Requests {
		for k := range req {
			if !allowed[k] {
				t.Fatalf("unexpected field %q in %v", k, req)
			}
		}
	}
}

// The pending list is rate-limited per address like the other pairing routes.
func TestDevicePendingIsRateLimited(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	store := discovery.NewMemoryPairingStore()
	h := &Handler{Store: store, Minter: &fakeMinter{}, TTL: time.Minute, NowFunc: func() time.Time { return now }}
	r := chi.NewRouter()
	h.MountDevice(r, 3)
	limited := false
	for i := 0; i < 10; i++ {
		code, _, _ := pending(t, r, &principal.Principal{UserID: "viewer-1"})
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("ten asks in a row were never limited")
	}
}

// The TV is told once a phone signed in on the home read its code ("your phone found
// this TV"); a cloud sign-in reading it (it cannot approve) does not count.
func TestDevicePollSaysWhenAPhoneFoundTheCode(t *testing.T) {
	_, _, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	lookedUp := func() bool {
		rec := poll(r, s.DeviceCode)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("poll = %d %s", rec.Code, rec.Body.String())
		}
		var got map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got["looked_up"] == true
	}
	if lookedUp() {
		t.Fatal("looked up before anyone read the code")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/pairing/device/"+s.Code, nil)
	req = req.WithContext(principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "viewer-1", CloudScope: "viewer"}))
	r.ServeHTTP(httptest.NewRecorder(), req)
	if lookedUp() {
		t.Fatal("a cloud sign-in's read counted")
	}
	if rec := lookup(r, s.Code, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("lookup = %d", rec.Code)
	}
	if !lookedUp() {
		t.Fatal("the TV was not told a phone found its code")
	}
}
