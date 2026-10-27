package telemetry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// On Postgres, with every migration applied (slot 0161 emptied the old sink and
// made the outbox): the outbox keeps, puts off, prunes and drops as the sender
// needs; the switch and the home's id live in app_settings; every query of the
// report runs against the real tables.
func TestOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	for _, old := range []string{"telemetry_events", "telemetry_web_vitals"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + old).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s holds %d rows (%v)", old, n, err)
		}
	}

	s := PGStore{DB: db}
	now := time.Now().UTC()
	if err := s.Add(ctx, KindApp, installID, map[string]string{"type": "app"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, KindServer, "HOME0000000000000000", map[string]string{"type": "server"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Due(ctx, now.Add(time.Second), 10)
	if err != nil || len(rows) != 2 || rows[0].Kind != KindApp || string(rows[0].Body) != `{"type": "app"}` {
		t.Fatalf("due = %+v, %v", rows, err)
	}
	if err := s.Retry(ctx, rows[0].ID, now.Add(time.Hour), "status 503"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Due(ctx, now.Add(time.Second), 10); len(rows) != 1 || rows[0].Kind != KindServer {
		t.Fatalf("a put-off row is due: %+v", rows)
	}
	if n, oldest, err := s.Pending(ctx, KindServer); err != nil || n != 1 || oldest == nil {
		t.Fatalf("pending = %d %v %v", n, oldest, err)
	}
	if err := s.DropKind(ctx, KindServer); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, KindForget, installID, map[string]string{"type": "forget"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DropInstall(ctx, installID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Due(ctx, now.Add(2*time.Hour), 10); len(rows) != 1 || rows[0].Kind != KindForget {
		t.Fatalf("after the drops: %+v", rows)
	}
	if _, err := db.Exec(`UPDATE telemetry_outbox SET created_at = now() - interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(ctx, now.Add(-MaxAge)); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if err := s.Sent(ctx, []int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	reader := runtimecfg.New(db)
	st := PGState{DB: db, Settings: reader}
	if st.Sharing(ctx) {
		t.Fatal("sharing before the admin turned it on")
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('telemetry.share_server_data', 'true')`); err != nil {
		t.Fatal(err)
	}
	reader.Invalidate()
	if !st.Sharing(ctx) {
		t.Fatal("the switch is on but reads off")
	}
	id, err := st.InstallID(ctx)
	if err != nil || !installIDRe.MatchString(id) {
		t.Fatalf("id %q, %v", id, err)
	}
	if again, _ := st.InstallID(ctx); again != id {
		t.Fatal("the home's id changed")
	}
	if fresh, _ := st.NewInstallID(ctx); fresh == id {
		t.Fatal("NewInstallID kept the id")
	}
	if err := st.SetLastReportDay(ctx, "2026-10-26"); err != nil || st.LastReportDay(ctx) != "2026-10-26" {
		t.Fatalf("last day %q, %v", st.LastReportDay(ctx), err)
	}

	// the hard rule: what names someone or something on this home never leaves
	// it, however it is spelled in the database
	secrets := []string{"fatima.sara", "fatima@example.org", "Sara's NAS", "Secret Lectures", "/srv/secret",
		"درس سري", "Quran Channel"}
	for _, q := range []string{
		`INSERT INTO users (id, username, pw_hash, email) VALUES (gen_random_uuid(), 'fatima.sara', 'x', 'fatima@example.org')`,
		`INSERT INTO app_settings (key, value) VALUES ('home.name', '"Sara''s NAS"')`,
		`INSERT INTO libraries (name, roots) VALUES ('Secret Lectures', ARRAY['/srv/secret'])`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v: %s", err, q)
		}
	}
	if _, err := db.Exec(`INSERT INTO videos (library_id, path, filename, title, content_hash, size_bytes, mtime)
		SELECT id, '/srv/secret/درس سري.mp4', 'درس سري.mp4', 'درس سري', repeat('a', 64), 1, now() FROM libraries LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO channels (name, number, slug, mode) VALUES ('Quran Channel', 7, 'quran-channel', 'shuffle')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO api_usage_daily (day, client_app, platform, requests, errors)
		VALUES (current_date, 'web', 'macos', 1500, 3), (current_date - 30, 'tv', 'tvos', 9, 0)`); err != nil {
		t.Fatal(err)
	}
	errs := &ErrorCounts{}
	errs.add("Secret Lectures failed")
	rep := (&Reporter{DB: db, Version: "1.2.3", Errors: errs}).Build(ctx, now, false)
	raw, _ := json.Marshal(rep)
	for _, secret := range secrets {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the report carries %q: %s", secret, raw)
		}
	}
	if rep.LibrarySize != "1" || rep.Features["channels"] != "1" || len(rep.Errors) != 1 || rep.Errors[0].Message != OtherError {
		t.Errorf("the counts: %+v", rep)
	}
	if rep.Users == "" || rep.Libraries != "1" || rep.Machines == "" ||
		len(rep.Apps) != 1 || rep.Apps[0] != (AppUse{App: "web", Platform: "macos", People: "0", Requests: "1k-10k", Errors: "2-5"}) {
		t.Fatalf("report = %+v", rep)
	}
	for _, f := range []string{"channels", "collections", "transcripts", "subtitle_files", "access_tokens",
		"paired_devices", "library_acl_rows", "extensions", "open_registration"} {
		if _, ok := rep.Features[f]; !ok {
			t.Errorf("feature %s missing: its query failed on the real schema", f)
		}
	}
}
