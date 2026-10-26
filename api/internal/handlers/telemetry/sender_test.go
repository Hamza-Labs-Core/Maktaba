package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// cloud is a fake Maktaba Cloud: it records each envelope and answers with
// status (a func of the envelope).
type cloud struct {
	mu        sync.Mutex
	envelopes []envelope
	status    func(envelope) int
}

type envelope struct {
	Schema  int               `json:"schema"`
	Batches []json.RawMessage `json:"batches"`
}

func (c *cloud) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != IngestPath || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var e envelope
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Errorf("envelope: %v", err)
		}
		c.mu.Lock()
		c.envelopes = append(c.envelopes, e)
		status := http.StatusNoContent
		if c.status != nil {
			status = c.status(e)
		}
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (c *cloud) batches() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, e := range c.envelopes {
		for _, b := range e.Batches {
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			out = append(out, m)
		}
	}
	return out
}

func newSender(t *testing.T, url string, at *time.Time) (*Sender, *memStore, *memState) {
	t.Helper()
	store, state := &memStore{}, &memState{}
	db := reportDB(t)
	s := &Sender{Store: store, State: state, URL: func() string { return url },
		Reporter: &Reporter{DB: db, Version: "1.2.3", Errors: &ErrorCounts{}},
		Now:      func() time.Time { return *at }}
	return s, store, state
}

// Off (the default), the home sends nothing of its own, keeps no error counts,
// and an app's batch still goes: the server switch is not the app's consent.
func TestSenderSwitchOff(t *testing.T) {
	c := &cloud{}
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s, store, _ := newSender(t, c.serve(t).URL, &now)
	s.Reporter.Errors.add("boom")
	_ = store.Add(context.Background(), KindServer, "X", map[string]string{"type": "server"}) // from before the switch went off
	_ = store.Add(context.Background(), KindApp, installID, map[string]string{"type": "app", "install_id": installID})
	s.Tick(context.Background())
	got := c.batches()
	if len(got) != 1 || got[0]["type"] != "app" {
		t.Fatalf("sent %v", got)
	}
	if len(store.kinds()) != 0 || len(s.Reporter.Errors.Take()) != 0 {
		t.Fatalf("outbox %v, or errors kept while off", store.kinds())
	}
}

// On, one report a day: the first tick of a day adds it, later ticks do not.
func TestSenderDailyReport(t *testing.T) {
	c := &cloud{}
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s, _, state := newSender(t, c.serve(t).URL, &now)
	state.sharing = true
	s.Reporter.Errors.add("scan_failed: open /srv/a/b.mp4: denied")
	s.Tick(context.Background())
	now = now.Add(3 * time.Hour)
	s.Tick(context.Background())
	got := c.batches()
	if len(got) != 1 || got[0]["type"] != "server" || got[0]["day"] != "2026-10-26" || got[0]["install_id"] == "" {
		t.Fatalf("sent %v", got)
	}
	rep := got[0]["report"].(map[string]any)
	if rep["library_size"] != "2-5" || rep["version"] != "1.2.3" || rep["install_kind"] == "" {
		t.Fatalf("report %v", rep)
	}
	errs := rep["errors"].([]any)
	if msg := errs[0].(map[string]any)["message"]; msg != "scan_failed: open <path>/<file>.mp4: denied" {
		t.Fatalf("error not scrubbed: %v", msg)
	}
	now = now.Add(24 * time.Hour)
	s.Tick(context.Background())
	if n := len(c.batches()); n != 2 {
		t.Fatalf("%d reports after the second day, want 2", n)
	}
	if s.Status().LastSentAt == nil {
		t.Fatal("no last delivery")
	}
}

// A cloud that is down or busy puts the batches off with a growing wait; a
// refused batch (4xx) is dropped alone, the others in its request go on.
func TestSenderRetriesAndRefusals(t *testing.T) {
	down := true
	c := &cloud{status: func(e envelope) int {
		if down {
			return http.StatusServiceUnavailable
		}
		for _, b := range e.Batches {
			if strings.Contains(string(b), "bad") {
				return http.StatusBadRequest
			}
		}
		return http.StatusNoContent
	}}
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s, store, _ := newSender(t, c.serve(t).URL, &now)
	ctx := context.Background()
	_ = store.Add(ctx, KindApp, installID, map[string]string{"type": "app", "n": "good1"})
	_ = store.Add(ctx, KindApp, installID, map[string]string{"type": "app", "n": "bad"})
	_ = store.Add(ctx, KindApp, installID, map[string]string{"type": "app", "n": "good2"})

	s.Tick(ctx)
	if len(store.kinds()) != 3 || store.rows[0].Attempts != 1 || !store.rows[0].nextAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after a 503: %+v", store.rows)
	}
	if s.Status().LastError != "status 503" {
		t.Fatalf("last error %q", s.Status().LastError)
	}
	s.Tick(ctx) // not due yet: nothing is sent
	if len(c.envelopes) != 1 {
		t.Fatalf("%d requests before the batches were due", len(c.envelopes))
	}
	now = now.Add(time.Minute)
	s.Tick(ctx)
	if store.rows[0].Attempts != 2 || !store.rows[0].nextAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("second backoff: %+v", store.rows[0])
	}

	down = false
	now = now.Add(2 * time.Minute)
	s.Tick(ctx)
	if len(store.kinds()) != 0 {
		t.Fatalf("left %v", store.kinds())
	}
	// the refused request, then each batch alone: the good ones delivered, the
	// bad one dropped
	last := c.envelopes[len(c.envelopes)-3:]
	for i, want := range []string{"good1", "bad", "good2"} {
		if len(last[i].Batches) != 1 || !strings.Contains(string(last[i].Batches[0]), want) {
			t.Fatalf("request %d: %s", i, last[i].Batches)
		}
	}
}

func TestBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{0: time.Minute, 1: 2 * time.Minute, 3: 8 * time.Minute, 20: maxBackoff} {
		if got := backoff(n); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

// A home without internet keeps nothing for long: a week-old batch goes unsent.
// Without a cloud to send to, nothing is attempted.
func TestSenderPrunesAndNoURL(t *testing.T) {
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s, store, _ := newSender(t, "", &now)
	_ = store.Add(context.Background(), KindApp, installID, map[string]string{"type": "app"})
	store.rows[0].CreatedAt = now.Add(-MaxAge - time.Hour)
	_ = store.Add(context.Background(), KindApp, installID, map[string]string{"type": "app"})
	store.rows[1].CreatedAt = now
	s.Tick(context.Background())
	if len(store.rows) != 1 || store.rows[0].Attempts != 0 {
		t.Fatalf("outbox %+v", store.rows)
	}
}

// One request carries what fits in the cloud's limit.
func TestFit(t *testing.T) {
	big := json.RawMessage(`"` + strings.Repeat("x", maxEnvelopeBytes/2) + `"`)
	rows := []Row{{Body: big}, {Body: big}, {Body: big}}
	if n := len(fit(rows)); n != 1 {
		t.Fatalf("fit kept %d", n)
	}
	if n := len(fit([]Row{{Body: json.RawMessage(`{}`)}, {Body: json.RawMessage(`{}`)}})); n != 2 {
		t.Fatalf("fit kept %d small rows", n)
	}
}
