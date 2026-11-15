package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
)

type fakeTurnstile struct {
	err   error
	calls int
}

func (f *fakeTurnstile) Verify(context.Context, string, string) error { f.calls++; return f.err }

type fakeAgents struct{}

func (fakeAgents) VerifySecret(_ context.Context, id, secret string) error {
	if id == "srv-1" && secret == "s3cret" {
		return nil
	}
	return errors.New("no")
}

func router(d *Deps) *chi.Mux {
	r := chi.NewRouter()
	MountPublic(r, d)
	MountHome(r, d)
	MountAdmin(r, d)
	return r
}

func send(r http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const goodNotice = `{"claimant_name":"Studio A","claimant_email":"rights@studio.test","claimant_address":"1 Road",
	"work":"Film X","urls":["https://watch.maktaba.tv/home-a/news"],"explanation":"the film","good_faith":true,
	"accuracy":true,"signature":"A. Person","turnstile_token":"tok"}`

// The public notice form refuses another site's page, checks the bot token on every request,
// names what a notice misses (T01) and refuses a notice about another site, all before the
// database is touched (the Store is nil here).
func TestPublicNoticeGuards(t *testing.T) {
	ts := &fakeTurnstile{}
	r := router(&Deps{Turnstile: ts})

	if rec := send(r, "POST", "/v1/moderation/notices", goodNotice, map[string]string{"Origin": "https://evil.test"}); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "origin_not_allowed") {
		t.Errorf("another origin = %d %s", rec.Code, rec.Body)
	}
	if ts.calls != 0 {
		t.Error("a refused origin asked Turnstile")
	}

	ts.err = turnstile.ErrFailed
	if rec := send(r, "POST", "/v1/moderation/notices", goodNotice, nil); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), turnstile.CodeFailed) {
		t.Errorf("a failed bot check (no Origin: a script) = %d %s", rec.Code, rec.Body)
	}
	ts.err = turnstile.ErrUnavailable
	if rec := send(r, "POST", "/v1/moderation/notices", goodNotice, map[string]string{"Origin": "https://maktaba.tv"}); rec.Code != http.StatusServiceUnavailable ||
		rec.Header().Get("Retry-After") == "" {
		t.Errorf("siteverify away = %d", rec.Code)
	}
	ts.err = nil

	rec := send(r, "POST", "/v1/moderation/notices", `{"claimant_name":"A","good_faith":true}`, map[string]string{"Origin": "https://maktaba.tv"})
	var body struct {
		Error   string   `json:"error"`
		Missing []string `json:"missing"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusUnprocessableEntity || body.Error != "notice_incomplete" || len(body.Missing) != 7 {
		t.Errorf("an incomplete notice = %d %+v", rec.Code, body)
	}

	other := strings.Replace(goodNotice, "https://watch.maktaba.tv/home-a/news", "https://videos.test/x", 1)
	if rec := send(r, "POST", "/v1/moderation/notices", other, nil); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "not_on_maktaba") {
		t.Errorf("a notice about another site = %d %s", rec.Code, rec.Body)
	}
}

// A report names a known category and a watch channel.
func TestPublicReportGuards(t *testing.T) {
	r := router(&Deps{})
	if rec := send(r, "POST", "/v1/moderation/reports", `{"category":"spam","channel":"https://watch.maktaba.tv/a/b"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown category = %d", rec.Code)
	}
	if rec := send(r, "POST", "/v1/moderation/reports", `{"category":"abuse","about":"x","channel":"https://watch.maktaba.tv/a/b"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown about = %d", rec.Code)
	}
	if rec := send(r, "POST", "/v1/moderation/reports", `{nope`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("not JSON = %d", rec.Code)
	}
}

// A home's routes need its own credential; the admin routes are 403 without the staff gate.
func TestHomeAndAdminRoutesAreGuarded(t *testing.T) {
	r := router(&Deps{Agents: fakeAgents{}})
	for _, rt := range [][2]string{{"GET", "/v1/broadcast/takedowns"}, {"POST", "/v1/broadcast/takedowns/x/ack"},
		{"GET", "/v1/broadcast/moderation"}, {"POST", "/v1/broadcast/moderation/counter-notices"}} {
		if rec := send(r, rt[0], rt[1], `{}`, nil); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s %s without a credential = %d", rt[0], rt[1], rec.Code)
		}
		req := httptest.NewRequest(rt[0], rt[1], strings.NewReader(`{}`))
		req.SetBasicAuth("srv-1", "wrong")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a wrong secret = %d", rt[0], rt[1], rec.Code)
		}
	}
	// a home with a good credential and no signing key: the pull is 503, an ack must name a result
	req := httptest.NewRequest("GET", "/v1/broadcast/takedowns", nil)
	req.SetBasicAuth("srv-1", "s3cret")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("the pull without the key = %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/v1/broadcast/takedowns/x/ack", strings.NewReader(`{"result":"maybe"}`))
	req.SetBasicAuth("srv-1", "s3cret")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an ack with no result = %d", rec.Code)
	}
	for _, rt := range [][2]string{{"GET", "/v1/admin/moderation"}, {"POST", "/v1/admin/moderation/actions"},
		{"POST", "/v1/admin/moderation/actions/x/restore"}, {"GET", "/v1/admin/moderation/export"}} {
		if rec := send(r, rt[0], rt[1], `{}`, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with no gate = %d", rt[0], rt[1], rec.Code)
		}
	}
}

// Every decision needs its reason, before the store is asked.
func TestDecisionsNeedAReason(t *testing.T) {
	d := &Deps{}
	pass := func(next http.Handler) http.Handler { return next }
	d.Gate = pass
	r := router(d)
	if rec := send(r, "POST", "/v1/admin/moderation/notices/x/reject", `{"reason":" "}`, nil); rec.Code != http.StatusForbidden {
		// no operator in the context: the handler refuses before reading the body
		t.Errorf("no operator = %d", rec.Code)
	}
	if _, ok := reasonedFrom(`{"reason":"  "}`); ok {
		t.Error("a blank reason passed")
	}
	if b, ok := reasonedFrom(`{"reason":"duplicate of n-1","unfounded":true}`); !ok || !b.Unfounded {
		t.Errorf("a reason = %+v %v", b, ok)
	}
}

func reasonedFrom(body string) (reasonBody, bool) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	return reasoned(httptest.NewRecorder(), req)
}
