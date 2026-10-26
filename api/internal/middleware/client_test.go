package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/usage"
	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// Client puts the parsed header on the context; no header reads unknown.
func TestClientMiddleware(t *testing.T) {
	var got client.Info
	h := Client(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = client.From(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(client.Header, "Remote/2.1 (Android; Foldable)")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if want := (client.Info{App: "remote", Version: "2.1", Platform: "android", Device: "foldable"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got != client.None {
		t.Errorf("no header: got %+v, want None", got)
	}
}

// Metrics counts each request under its app, a bounded label.
func TestMetricsCountsByClient(t *testing.T) {
	h := Client(Metrics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})))
	before := counterValue(t, metrics.HTTPRequestsByClient.WithLabelValues("tv", "5xx"))
	beforeOther := counterValue(t, metrics.HTTPRequestsByClient.WithLabelValues("other", "5xx"))
	for _, v := range []string{"tv/1.0 (tvos; tv)", "tv/1.1 (androidtv; tv)", "made-up/1 (x; y)"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(client.Header, v)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := counterValue(t, metrics.HTTPRequestsByClient.WithLabelValues("tv", "5xx")) - before; got != 2 {
		t.Errorf("tv 5xx: +%v, want +2", got)
	}
	if got := counterValue(t, metrics.HTTPRequestsByClient.WithLabelValues("other", "5xx")) - beforeOther; got != 1 {
		t.Errorf("other 5xx: +%v, want +1", got)
	}
}

// counterValue reads one counter of the vector.
func counterValue(t *testing.T, c interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

// Usage counts every request with its app, platform and person; an error is a
// 5xx; an event stream's time is not counted.
func TestUsageMiddleware(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		`CREATE TABLE api_usage_daily (day TEXT, client_app TEXT, platform TEXT, requests INTEGER, errors INTEGER,
			total_ms INTEGER, slow INTEGER, PRIMARY KEY (day, client_app, platform))`,
		`CREATE TABLE api_usage_people_daily (day TEXT, client_app TEXT, user_id TEXT, client_version TEXT,
			PRIMARY KEY (day, client_app, user_id))`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	c := &usage.Counter{}
	c.Attach(db)
	h := Client(Usage(c)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(http.StatusBadGateway)
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
		}
	})))
	const user = "00000000-0000-4000-8000-0000000000a1"
	for _, path := range []string{"/ok", "/fail", "/events"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(client.Header, "player/1.3.0 (ipados; tablet)")
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: user}))
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil)) // no header, no one
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var requests, errors int
	if err := db.QueryRow(`SELECT requests, errors FROM api_usage_daily WHERE client_app = 'player' AND platform = 'ipados'`).
		Scan(&requests, &errors); err != nil || requests != 3 || errors != 1 {
		t.Errorf("player: %d requests, %d errors (%v); want 3, 1", requests, errors, err)
	}
	if err := db.QueryRow(`SELECT requests FROM api_usage_daily WHERE client_app = 'unknown' AND platform = 'unknown'`).
		Scan(&requests); err != nil || requests != 1 {
		t.Errorf("unknown: %d requests (%v); want 1", requests, err)
	}
	var who, version string
	if err := db.QueryRow(`SELECT user_id, client_version FROM api_usage_people_daily WHERE client_app = 'player'`).
		Scan(&who, &version); err != nil || who != user || version != "1.3.0" {
		t.Errorf("people: %q %q (%v)", who, version, err)
	}
	// and no row names a path
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM api_usage_daily WHERE client_app LIKE '%/%' OR platform LIKE '%/%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a path in the rows: %d (%v)", n, err)
	}
	// nil counter: the handler as is
	if Usage(nil)(http.NotFoundHandler()) == nil {
		t.Error("Usage(nil) lost the handler")
	}
}
