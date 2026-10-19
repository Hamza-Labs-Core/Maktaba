package tv

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

type enqueued struct {
	stage string
	force bool
	why   any
}

// newPlexServer adds the slot-0116 plex_livetv_state table and the
// extensions row to the tv test database, with a fake enqueue.
func newPlexServer(t *testing.T, outcome string) (*sql.DB, http.Handler, *[]enqueued) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tv.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE hdhr_device (
			id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1), device_id TEXT NOT NULL,
			device_uuid TEXT NOT NULL, friendly_name TEXT NOT NULL DEFAULT 'Maktaba',
			tuner_count INTEGER NOT NULL DEFAULT 4, enabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`,
		// shared/db/migrations/0116_plex_livetv.sqlite.sql
		`CREATE TABLE plex_livetv_state (
			id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1), register_requested_at TEXT,
			requested_base_url TEXT, status TEXT NOT NULL DEFAULT '{}', checked_at TEXT,
			guide_reloaded_at TEXT, guide_version TEXT, channelmap_at TEXT,
			channelmap_hash TEXT, last_error TEXT, last_error_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`,
		`CREATE TABLE extensions (name TEXT PRIMARY KEY, enabled INTEGER NOT NULL,
			settings TEXT NOT NULL, updated_at TEXT)`,
		`CREATE TABLE job_stages (stage TEXT PRIMARY KEY, enabled INTEGER NOT NULL,
			disabled_reason TEXT)`,
		`INSERT INTO extensions VALUES ('plex', 1, '{"url":"http://plex.lan:32400","token":"secret"}', NULL)`,
		`INSERT INTO job_stages VALUES ('plex_livetv', 1, NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	calls := &[]enqueued{}
	r := chi.NewRouter()
	(&Handler{
		DB: db, PublicBaseURL: "http://192.168.1.20:8782", DefaultStreamPort: "8782",
		NowFunc: func() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) },
		Enqueue: func(_ context.Context, stage string, force bool, reason map[string]any) (string, *int64, error) {
			*calls = append(*calls, enqueued{stage, force, reason["why"]})
			id := int64(len(*calls))
			return outcome, &id, nil
		},
	}).Mount(r)
	return db, r, calls
}

func enableTuner(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO hdhr_device (id, device_id, device_uuid, enabled)
		VALUES (1, 'A1B2C3D4', 'u', 1) ON CONFLICT (id) DO UPDATE SET enabled = 1`); err != nil {
		t.Fatal(err)
	}
}

func TestPlexEndpointsAdminOnly(t *testing.T) {
	_, srv, calls := newPlexServer(t, "inserted")
	for _, c := range [][2]string{
		{"GET", "/api/tv/plex"}, {"POST", "/api/tv/plex/refresh"}, {"POST", "/api/tv/plex/register"},
	} {
		if code, _ := do(t, srv, c[0], c[1], "", user); code != http.StatusForbidden {
			t.Errorf("user %s %s: %d", c[0], c[1], code)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may be queued: %v", *calls)
	}
}

func TestPlexStatusBeforeAnyRun(t *testing.T) {
	_, srv, _ := newPlexServer(t, "inserted")
	code, out := do(t, srv, "GET", "/api/tv/plex", "", admin)
	if code != 200 {
		t.Fatalf("code %d %v", code, out)
	}
	plex, _ := out["plex"].(map[string]any)
	if plex["configured"] != true || plex["enabled"] != true || plex["url"] != "http://plex.lan:32400" {
		t.Fatalf("plex: %v", plex)
	}
	if _, leaked := plex["token"]; leaked {
		t.Fatal("the token must never be returned")
	}
	if out["checked_at"] != nil || out["job"] != nil {
		t.Fatalf("no run yet: %v", out)
	}
	if st, _ := out["stage"].(map[string]any); st["enabled"] != true {
		t.Fatalf("stage: %v", out["stage"])
	}
}

func TestPlexStatusReportsTheStagesState(t *testing.T) {
	db, srv, _ := newPlexServer(t, "inserted")
	if _, err := db.Exec(`INSERT INTO plex_livetv_state (id, status, checked_at, guide_reloaded_at,
		channelmap_at, last_error) VALUES (1, '{"reachable":true,"maktaba":{"registered":true,"dvr_key":"12"},
		"channels":{"lineup":3,"mapped":3}}', '2026-10-18T08:00:00.000Z', '2026-10-18T07:00:00.000Z',
		'2026-10-18T06:00:00.000Z', NULL)`); err != nil {
		t.Fatal(err)
	}
	_, out := do(t, srv, "GET", "/api/tv/plex", "", admin)
	st, _ := out["status"].(map[string]any)
	mk, _ := st["maktaba"].(map[string]any)
	if st["reachable"] != true || mk["registered"] != true || mk["dvr_key"] != "12" {
		t.Fatalf("status: %v", st)
	}
	if out["checked_at"] != "2026-10-18T08:00:00.000Z" || out["guide_reloaded_at"] != "2026-10-18T07:00:00.000Z" ||
		out["channelmap_at"] != "2026-10-18T06:00:00.000Z" || out["last_error"] != nil {
		t.Fatalf("times: %v", out)
	}
}

func TestPlexRefreshQueuesTheStageForced(t *testing.T) {
	_, srv, calls := newPlexServer(t, "inserted")
	code, out := do(t, srv, "POST", "/api/tv/plex/refresh", "", admin)
	if code != http.StatusAccepted || out["queued"] != true || out["outcome"] != "inserted" {
		t.Fatalf("refresh: %d %v", code, out)
	}
	if len(*calls) != 1 || (*calls)[0] != (enqueued{"plex_livetv", true, "plex_refresh"}) {
		t.Fatalf("enqueued: %v", *calls)
	}
}

func TestPlexRefreshRefusedWithoutPlexOrStage(t *testing.T) {
	db, srv, _ := newPlexServer(t, "disabled")
	if code, _ := do(t, srv, "POST", "/api/tv/plex/refresh", "", admin); code != http.StatusConflict {
		t.Fatalf("stage off: %d", code)
	}
	if _, err := db.Exec(`UPDATE extensions SET settings = '{"url":"http://plex.lan:32400"}'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(t, srv, "POST", "/api/tv/plex/refresh", "", admin); code != http.StatusConflict {
		t.Fatalf("no token: %d", code)
	}
}

func TestPlexRegisterRecordsTheRequestAndQueues(t *testing.T) {
	db, srv, calls := newPlexServer(t, "inserted")
	enableTuner(t, db)
	code, out := do(t, srv, "POST", "/api/tv/plex/register", "", admin)
	if code != http.StatusAccepted {
		t.Fatalf("register: %d %v", code, out)
	}
	var at, base sql.NullString
	if err := db.QueryRow(`SELECT register_requested_at, requested_base_url FROM plex_livetv_state`).
		Scan(&at, &base); err != nil {
		t.Fatal(err)
	}
	if at.String != "2026-10-18T09:00:00.000Z" || base.String != "http://192.168.1.20:8782" {
		t.Fatalf("request: %v %v", at, base)
	}
	if len(*calls) != 1 || (*calls)[0] != (enqueued{"plex_livetv", true, "plex_register"}) {
		t.Fatalf("enqueued: %v", *calls)
	}
	// asked twice before the stage ran: still one row, the request kept
	if code, _ := do(t, srv, "POST", "/api/tv/plex/register", "", admin); code != http.StatusAccepted {
		t.Fatalf("second: %d", code)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM plex_livetv_state`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows %d", n)
	}
}

func TestPlexRegisterRefusesWhenRegisteredOrTunerOff(t *testing.T) {
	db, srv, calls := newPlexServer(t, "inserted")
	if code, out := do(t, srv, "POST", "/api/tv/plex/register", "", admin); code != http.StatusConflict {
		t.Fatalf("tuner off: %d %v", code, out)
	}
	enableTuner(t, db)
	if _, err := db.Exec(`INSERT INTO plex_livetv_state (id, status) VALUES
		(1, '{"maktaba":{"registered":true}}')`); err != nil {
		t.Fatal(err)
	}
	code, out := do(t, srv, "POST", "/api/tv/plex/register", "", admin)
	if code != http.StatusConflict {
		t.Fatalf("already registered: %d %v", code, out)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may be queued: %v", *calls)
	}
	var at sql.NullString
	_ = db.QueryRow(`SELECT register_requested_at FROM plex_livetv_state`).Scan(&at)
	if at.Valid {
		t.Fatal("a refused register must not be recorded")
	}
}

func TestHDHRPatchNotifiesPlex(t *testing.T) {
	_, srv, calls := newPlexServer(t, "inserted")
	if code, _ := do(t, srv, "PATCH", "/api/tv/hdhr", `{"friendly_name":"Maktaba TV"}`, admin); code != 200 {
		t.Fatalf("patch %d", code)
	}
	if len(*calls) != 1 || (*calls)[0] != (enqueued{"plex_livetv", false, "lineup_edited"}) {
		t.Fatalf("enqueued: %v", *calls)
	}
}

// Since slot 0120 the Plex token is in home_secrets, not in the settings
// row: the handler counts it there (story 32.6 AC6a).
func TestPlexConfiguredFromHomeSecrets(t *testing.T) {
	db, _, _ := newPlexServer(t, "inserted")
	if _, err := db.Exec(`UPDATE extensions SET settings = '{"url":"http://plex.lan:32400"}'`); err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	h := &Handler{DB: db, Secrets: store}
	if h.plexExtension(context.Background()).Configured {
		t.Fatal("no token anywhere: not configured")
	}
	_ = store.Put(context.Background(), secrets.ExtensionName("plex", "token"), []byte("tok"), "")
	if e := h.plexExtension(context.Background()); !e.Configured || e.URL != "http://plex.lan:32400" {
		t.Fatalf("token in home_secrets: %+v", e)
	}
}
