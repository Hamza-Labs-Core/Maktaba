package telemetry

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// On Postgres, with every migration applied (slot 0156's JSONB payload, the
// libraries' roots from library_roots): an event is stored redacted, the sweep
// keeps what is in its window, and forgetting the device leaves none of its rows.
func TestSinkOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`WITH l AS (INSERT INTO libraries (name, roots) VALUES ('Lectures', ARRAY['/srv/Lectures'])
		RETURNING id) INSERT INTO library_roots (library_id, path, path_canonical)
		SELECT id, '/srv/Lectures', '/srv/Lectures' FROM l ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	h := New(db, true, nil)
	r := chi.NewRouter()
	h.Mount(r)
	f := &fixture{db: db, h: h, r: r}

	rec := f.do(t, http.MethodPost, "/api/telemetry", events(
		ev("error.uncaught", map[string]any{"error_message": "cannot open /srv/Lectures/foo.mp4", "x": 1.0}),
		ev("app.open", map[string]any{"cold_start": true}),
	), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var msg string
	if err := db.QueryRow(`SELECT payload->>'error_message' FROM telemetry_events WHERE event_kind = 'error.uncaught'`).Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if msg != "cannot open <library>/foo.mp4" {
		t.Fatalf("error_message = %q", msg)
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{
		"device_pseudonym": pseud, "metrics": []any{map[string]any{"metric": "INP", "value": 64.0, "route": "/videos/123"}},
	}, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("vitals: status %d: %s", rec.Code, rec.Body)
	}

	// a row 91 days old goes, the fresh ones stay
	if _, err := db.Exec(`INSERT INTO telemetry_events (received_at, device_pseudonym, event_kind) VALUES (now() - interval '91 days', $1, 'app.open')`, pseud); err != nil {
		t.Fatal(err)
	}
	evs, vit, err := (&Sweeper{DB: db}).RunOnce(context.Background())
	if err != nil || evs != 1 || vit != 0 {
		t.Fatalf("sweep: %d events, %d vitals, %v", evs, vit, err)
	}
	if n := f.count(t, "telemetry_events"); n != 2 {
		t.Fatalf("%d events after the sweep, want 2", n)
	}

	if rec := f.do(t, http.MethodDelete, "/api/telemetry/devices/"+pseud, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body)
	}
	if n := f.count(t, "telemetry_events") + f.count(t, "telemetry_web_vitals"); n != 0 {
		t.Fatalf("%d rows after forgetting the device", n)
	}
}
