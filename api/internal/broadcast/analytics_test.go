package broadcast

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type fakeAnalytics struct {
	raw          json.RawMessage
	err          error
	period, zone string
	n            int
}

func (f *fakeAnalytics) Analytics(_ context.Context, period, tz string) (json.RawMessage, error) {
	f.n++
	f.period, f.zone = period, tz
	return f.raw, f.err
}

const homeReport = `{"level":"full","live_viewers":3,"hours":{"today":10,"week":40,"month":90,"allowance":5000},` +
	`"countries":[{"key":"EG","value":12},{"key":"other","value":2}]}`

func analyticsCall(svc *Service, p *principal.Principal, query string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	(&Routes{Service: svc}).Mount(r)
	req := httptest.NewRequest("GET", "/api/tv/broadcast/analytics"+query, nil)
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Story 50.28, the home's view: an admin gets the cloud's report as it came (folded there, Other
// kept), unclaimed without the cloud link and unreachable when it is away; the period and zone
// are checked before the cloud is asked.
func TestAnalyticsRoute(t *testing.T) {
	adminP := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true}

	if rec := analyticsCall(&Service{}, adminP, ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"cloud":"unclaimed"}` {
		t.Fatalf("unclaimed: %d %s", rec.Code, rec.Body)
	}
	f := &fakeAnalytics{raw: json.RawMessage(homeReport)}
	rec := analyticsCall(&Service{Analytics: f}, adminP, "?period=7d&tz="+url.QueryEscape("Africa/Cairo"))
	var v struct {
		Cloud  string          `json:"cloud"`
		Report json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != http.StatusOK || v.Cloud != "ok" {
		t.Fatalf("with the cloud: %d %s", rec.Code, rec.Body)
	}
	var got, want any
	_ = json.Unmarshal(v.Report, &got)
	_ = json.Unmarshal([]byte(homeReport), &want)
	if gb, _ := json.Marshal(got); string(gb) != mustJSON(want) {
		t.Fatalf("the report changed on the way: %s", v.Report)
	}
	if f.period != "7d" || f.zone != "Africa/Cairo" {
		t.Fatalf("asked the cloud for %q in %q", f.period, f.zone)
	}
	if rec := analyticsCall(&Service{Analytics: &fakeAnalytics{raw: json.RawMessage(homeReport)}}, adminP, ""); rec.Code != http.StatusOK {
		t.Fatalf("default period: %d", rec.Code)
	}
	away := &fakeAnalytics{err: ErrUnreachable}
	if rec := analyticsCall(&Service{Analytics: away}, adminP, ""); rec.Code != http.StatusOK ||
		strings.TrimSpace(rec.Body.String()) != `{"cloud":"unreachable"}` {
		t.Fatalf("the cloud away: %d %s", rec.Code, rec.Body)
	}
	refused := &fakeAnalytics{err: &CloudError{Status: http.StatusForbidden, Code: "broadcast_analytics_forbidden"}}
	if rec := analyticsCall(&Service{Analytics: refused}, adminP, ""); !strings.Contains(rec.Body.String(), `"unreachable"`) {
		t.Fatalf("a cloud refusal: %d %s", rec.Code, rec.Body)
	}

	bad := &fakeAnalytics{raw: json.RawMessage(homeReport)}
	for _, q := range []string{"?period=day", "?period=year", "?tz=Mars%2FOlympus", "?tz=" + strings.Repeat("A", 80)} {
		if rec := analyticsCall(&Service{Analytics: bad}, adminP, q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, rec.Code)
		}
	}
	if bad.n != 0 {
		t.Fatalf("a bad ask reached the cloud: %d", bad.n)
	}
}

// Story 50.28 T07: only the home's admins see who watches; the refusal comes before the cloud is
// asked (TestBroadcastRoutesAreAdminOnly covers it with every route; this names the fake).
func TestAnalyticsIsAdminOnly(t *testing.T) {
	f := &fakeAnalytics{raw: json.RawMessage(homeReport)}
	for _, c := range []struct {
		who  string
		p    *principal.Principal
		want int
	}{
		{"no one", nil, http.StatusUnauthorized},
		{"viewer", &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}, http.StatusForbidden},
	} {
		if rec := analyticsCall(&Service{Analytics: f}, c.p, "?period=today"); rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.who, rec.Code, c.want)
		}
	}
	if f.n != 0 {
		t.Fatalf("a refused caller reached the cloud: %d", f.n)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
