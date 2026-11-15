package broadcast

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// T09 (and the acceptance's PUT): a user who is not an admin gets 403 on every Broadcast
// route, no one at all 401, before anything is read or sent to the cloud.
func TestBroadcastRoutesAreAdminOnly(t *testing.T) {
	cloud := &fakeCloud{}
	r := chi.NewRouter()
	(&Routes{Service: &Service{Store: &Store{}, Cloud: cloud}}).Mount(r)
	param := regexp.MustCompile(`\{[^}]+\}`)
	for _, route := range Paths {
		method, pattern, _ := strings.Cut(route, " ")
		path := param.ReplaceAllString(pattern, "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60")
		for _, c := range []struct {
			who  string
			p    *principal.Principal
			want int
		}{
			{"no one", nil, http.StatusUnauthorized},
			{"viewer", &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}, http.StatusForbidden},
		} {
			req := httptest.NewRequest(method, path, strings.NewReader(`{"confirm_rights":true}`))
			if c.p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("%s as %s: %d, want %d", route, c.who, rec.Code, c.want)
			}
		}
	}
	if cloud.calls() != 0 {
		t.Fatalf("a refused caller reached the cloud: %d calls", cloud.calls())
	}
}

// The moderation routes for an admin (story 50.24): the records the cloud sent and the
// takedowns applied here; unclaimed without the cloud link, unreachable when it is away; a
// counter-notice's refusal from the cloud names what to fix.
func TestModerationRoutes(t *testing.T) {
	adminP := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true}
	call := func(svc *Service, method, path, body string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		(&Routes{Service: svc}).Mount(r)
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(principal.WithPrincipal(req.Context(), adminP))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(&Service{}, "GET", "/api/tv/broadcast/moderation", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"cloud":"unclaimed"`) || !strings.Contains(rec.Body.String(), `"takedowns":[]`) {
		t.Fatalf("unclaimed: %d %s", rec.Code, rec.Body)
	}
	mod := &fakeModeration{}
	if rec := call(&Service{Moderation: mod}, "GET", "/api/tv/broadcast/moderation", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"cloud":"ok"`) || !strings.Contains(rec.Body.String(), `"records":{}`) {
		t.Fatalf("with the cloud: %d %s", rec.Code, rec.Body)
	}
	if rec := call(&Service{Moderation: refusingModeration{status: http.StatusBadGateway}}, "GET", "/api/tv/broadcast/moderation", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"cloud":"unreachable"`) || strings.Contains(rec.Body.String(), "records") {
		t.Fatalf("the cloud away: %d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		svc  *Service
		body string
		want int
		code string
	}{
		{&Service{}, `{"action_id":"a-1"}`, http.StatusConflict, "broadcast.unclaimed"},
		{&Service{Moderation: mod}, `nope`, http.StatusBadRequest, ""},
		{&Service{Moderation: refusingModeration{status: http.StatusUnprocessableEntity}}, `{}`, http.StatusBadRequest, "broadcast.counterNoticeIncomplete"},
		{&Service{Moderation: refusingModeration{status: http.StatusConflict}}, `{}`, http.StatusConflict, "broadcast.counterNoticeFiled"},
		{&Service{Moderation: refusingModeration{status: http.StatusNotFound}}, `{}`, http.StatusConflict, "broadcast.counterNoticeUnknown"},
		{&Service{Moderation: refusingModeration{status: 0}}, `{}`, http.StatusBadGateway, "broadcast.cloudUnreachable"},
		{&Service{Moderation: refusingModeration{ok: true}}, `{"action_id":"a-1"}`, http.StatusCreated, ""},
	}
	for i, c := range cases {
		rec := call(c.svc, "POST", "/api/tv/broadcast/moderation/counter-notices", c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.code) {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

// refusingModeration answers every call with one cloud status (0: unreachable), or ok.
type refusingModeration struct {
	status int
	ok     bool
}

func (f refusingModeration) err() error {
	if f.status == 0 || f.status == http.StatusBadGateway {
		return ErrUnreachable
	}
	return &CloudError{Status: f.status}
}

func (f refusingModeration) Takedowns(context.Context) (string, error) { return "", f.err() }
func (f refusingModeration) AckTakedown(context.Context, string, TakedownAck) error {
	return f.err()
}
func (f refusingModeration) Moderation(context.Context) (json.RawMessage, error) {
	return nil, f.err()
}
func (f refusingModeration) CounterNotice(context.Context, json.RawMessage) (json.RawMessage, error) {
	if f.ok {
		return json.RawMessage(`{"id":"c-1"}`), nil
	}
	return nil, f.err()
}
