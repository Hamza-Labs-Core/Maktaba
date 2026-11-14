//go:build integration

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// Story 50.28 against Postgres: the hourly rollup from a fake Analytics Engine SQL API (T02), the
// daily check against metering (T03), the maktaba_broadcast_* series on the api's /metrics (T06),
// and who may read a home's analytics (T07, a test per rule).

// fakeAE answers the rollup's three queries from fixed rows.
type fakeAE struct{ hour, peaks, minute []broadcast.Row }

func (f fakeAE) Query(_ context.Context, sql string) ([]broadcast.Row, error) {
	switch {
	case strings.Contains(sql, "toStartOfInterval"):
		return f.peaks, nil
	case strings.Contains(sql, "IN ('beat', 'ingest')"):
		return f.minute, nil
	default:
		return f.hour, nil
	}
}

// egyptAndGermany is an hour with 12 sessions from Egypt and 2 from Germany.
func egyptAndGermany(pub string) fakeAE {
	return fakeAE{
		hour: []broadcast.Row{
			{"event": "play", "publisher": pub, "channel": "c1", "country": "EG", "device": "web", "n": "12", "startup_ms": 12000.0},
			{"event": "play", "publisher": pub, "channel": "c1", "country": "DE", "device": "web", "n": "2", "startup_ms": 2000.0},
			{"event": "beat", "publisher": pub, "channel": "c1", "country": "EG", "device": "web", "rung": "720p", "n": "400", "minutes": 400.0},
			{"event": "error", "publisher": pub, "channel": "c1", "code": "storage_unavailable", "n": "2"},
			{"event": "blocked", "publisher": pub, "channel": "c1", "code": "full", "n": "1"},
			{"event": "ingest", "publisher": pub, "channel": "c1", "rung": "720p", "n": "900", "segment_bytes": 1e9},
		},
		peaks: []broadcast.Row{{"publisher": pub, "channel": "c1", "minute": "x", "n": "9"}},
		minute: []broadcast.Row{
			{"event": "beat", "publisher": pub, "channel": "c1", "n": "7"},
			{"event": "ingest", "publisher": pub, "channel": "c1", "n": "15", "lag_ms": 15000.0,
				"le_0": "0", "le_1": "3", "le_2": "10", "le_3": "14", "le_4": "15", "le_5": "15", "le_6": "15", "le_7": "15"},
		},
	}
}

func (e *env) homeOf(ownerEmail, slug, plan string) string {
	e.t.Helper()
	var id string
	if err := e.pool.DB.QueryRowContext(context.Background(),
		`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan) VALUES ($1, 'Home', $2, 'x', $3) RETURNING id::text`,
		e.userID(ownerEmail), slug, plan).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestBroadcastAnalytics(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.registerVerified("publisher@example.test")
	home := e.homeOf("publisher@example.test", "pub-home", "pro")
	// 50.20's column (servers.broadcast_plan): the Broadcaster tier sees everything
	e.exec(`ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_plan TEXT`)
	e.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, home)

	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	day := hour.Format("2006-01-02")
	jobs := &broadcast.Jobs{DB: e.pool.DB, AE: egyptAndGermany(home), Logger: slog.Default()}
	if n, err := jobs.RollHour(ctx, hour); err != nil || n != 1 {
		t.Fatalf("RollHour = %d, %v", n, err)
	}
	// a re-run replaces the hour, never doubles it
	if _, err := jobs.RollHour(ctx, hour); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM broadcast_usage_hourly WHERE publisher_id = $1`, home); n != 1 {
		t.Fatalf("hourly rows %d", n)
	}
	path := "/v1/broadcast/analytics?server=" + home + "&period=day&date=" + day + "&tz=UTC"

	t.Run("the owner sees Egypt 12 and Other 2, Germany unnamed (T02, acceptance 1)", func(t *testing.T) {
		r := owner.mustStatus(owner.do("GET", path, nil), http.StatusOK)
		m := r.json(t)
		if m["level"] != "full" || m["sessions"].(float64) != 14 || m["peak"].(float64) != 9 || m["period_hours"].(float64) != 6.67 {
			t.Fatalf("report %s", r.body)
		}
		cs := m["countries"].([]any)
		if len(cs) != 2 || cs[0].(map[string]any)["key"] != "EG" || cs[0].(map[string]any)["value"].(float64) != 12 ||
			cs[1].(map[string]any)["key"] != "other" || cs[1].(map[string]any)["value"].(float64) != 2 {
			t.Fatalf("countries %v", cs)
		}
		if bytes.Contains(r.body, []byte(`"DE"`)) {
			t.Fatalf("Germany is named: %s", r.body)
		}
		if m["hours"].(map[string]any)["allowance"].(float64) != 5000 {
			t.Fatalf("allowance %v", m["hours"])
		}
	})

	t.Run("CSV export", func(t *testing.T) {
		r := owner.mustStatus(owner.do("GET", path+"&format=csv", nil), http.StatusOK)
		if !strings.HasPrefix(r.header.Get("Content-Type"), "text/csv") || !bytes.Contains(r.body, []byte("country,EG,12")) ||
			bytes.Contains(r.body, []byte("DE")) {
			t.Fatalf("csv %s %s", r.header.Get("Content-Type"), r.body)
		}
	})

	t.Run("T07: a user who doesn't own the home is 403", func(t *testing.T) {
		other := e.registerVerified("stranger@example.test")
		r := other.do("GET", path, nil)
		if r.code != http.StatusForbidden || r.errCode() != "broadcast_analytics_forbidden" {
			t.Fatalf("stranger: %d %s", r.code, r.body)
		}
		// a linked account (a member of the home, not its owner) is no publisher either
		e.exec(`INSERT INTO home_links (server_id, cloud_user_id) VALUES ($1, $2)`, home, e.userID("stranger@example.test"))
		if r := other.do("GET", path, nil); r.code != http.StatusForbidden {
			t.Fatalf("linked member: %d %s", r.code, r.body)
		}
		// a home that doesn't exist reads the same as someone else's
		if r := other.do("GET", "/v1/broadcast/analytics?server=00000000-0000-0000-0000-000000000000", nil); r.code != http.StatusForbidden {
			t.Fatalf("unknown home: %d", r.code)
		}
	})

	t.Run("T07: no token is 401, a bad server 400", func(t *testing.T) {
		anon := e.client()
		if r := anon.do("GET", path, nil); r.code != http.StatusUnauthorized {
			t.Fatalf("anonymous: %d", r.code)
		}
		if r := owner.do("GET", "/v1/broadcast/analytics?server=not!an!id", nil); r.code != http.StatusBadRequest {
			t.Fatalf("bad server: %d", r.code)
		}
		if r := owner.do("GET", strings.Replace(path, "tz=UTC", "tz=Mars/Base", 1), nil); r.code != http.StatusBadRequest {
			t.Fatalf("bad zone: %d", r.code)
		}
	})

	t.Run("Pro's guest sharing sees live viewers and hours only", func(t *testing.T) {
		e.registerVerified("guest-host@example.test")
		pro := e.homeOf("guest-host@example.test", "guest-home", "pro")
		g := e.client()
		g.login("guest-host@example.test", goodPassword)
		m := g.mustStatus(g.do("GET", "/v1/broadcast/analytics?server="+pro, nil), http.StatusOK).json(t)
		if m["level"] != "hours" || m["countries"] != nil || m["peak"] != nil || m["hours"].(map[string]any)["allowance"].(float64) != 50 {
			t.Fatalf("pro report %v", m)
		}
	})

	t.Run("T07: the console's route takes a service admin, audited; a customer token is refused", func(t *testing.T) {
		adminPath := "/v1/admin/broadcast/analytics?server=" + home + "&period=day&date=" + day
		if r := owner.do("GET", adminPath, nil); r.code != http.StatusUnauthorized && r.code != http.StatusForbidden {
			t.Fatalf("a customer on the console: %d %s", r.code, r.body)
		}
		ops := e.serviceAdmin("ops@example.test")
		m := ops.mustStatus(ops.do("GET", adminPath, nil), http.StatusOK).json(t)
		if m["level"] != "full" {
			t.Fatalf("admin report %v", m)
		}
		if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = $1 AND target_id = $2`, "admin.broadcast_analytics.view", home); n != 1 {
			t.Fatalf("audit rows %d", n)
		}
		if r := ops.do("GET", "/v1/admin/broadcast/analytics?server=00000000-0000-0000-0000-000000000000", nil); r.code != http.StatusNotFound {
			t.Fatalf("admin, unknown home: %d", r.code)
		}
	})

	t.Run("T06: the api's /metrics has the maktaba_broadcast_* series for the witness's scrape only; live viewers reach the owner", func(t *testing.T) {
		if err := jobs.PollMinute(ctx); err != nil {
			t.Fatal(err)
		}
		// a second worker polling the same minute adds nothing
		if err := jobs.PollMinute(ctx); err != nil {
			t.Fatal(err)
		}
		if err := broadcast.RecordRevocations(ctx, e.pool.DB, time.Now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		// the witness scrapes a node by its private address: the broadcast series are there
		req := httptest.NewRequest("GET", "/metrics", nil)
		req.Host = "10.50.0.2:9443"
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		out := rec.Body.String()
		// and never on a public name
		pub := httptest.NewRequest("GET", "/metrics", nil)
		pub.Host = "api.example.test"
		prec := httptest.NewRecorder()
		e.router.ServeHTTP(prec, pub)
		if prec.Code != http.StatusOK || strings.Contains(prec.Body.String(), "maktaba_broadcast_") {
			t.Fatalf("public /metrics: %d, broadcast series present: %v", prec.Code, strings.Contains(prec.Body.String(), "maktaba_broadcast_"))
		}
		for _, want := range []string{
			"maktaba_broadcast_channels_live 1",
			"maktaba_broadcast_viewers 7",
			"maktaba_broadcast_viewer_minutes_total 7",
			`maktaba_broadcast_ingest_lag_seconds_bucket{le="2"} 14`,
			"maktaba_broadcast_ingest_lag_seconds_count 15",
			"maktaba_broadcast_revocation_list_age_seconds",
			"maktaba_broadcast_edge_cost_estimate_usd",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("/metrics lacks %q", want)
			}
		}
		m := owner.mustStatus(owner.do("GET", path, nil), http.StatusOK).json(t)
		if m["live_viewers"].(float64) != 7 {
			t.Fatalf("live viewers %v", m["live_viewers"])
		}
	})
}

// T03 and acceptance 2: a day the rollup and metering differ by 3 % raises a SEV3 (an error line
// with code broadcast_metering_mismatch, which 50.10's rule pages on); 1 % stays quiet; the day is
// checked once.
func TestBroadcastCrossCheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// 50.20's metering windows (broadcast_usage_windows), as its migration makes them
	e.exec(`CREATE TABLE IF NOT EXISTS broadcast_usage_windows (publisher_id UUID NOT NULL, window_start TIMESTAMPTZ NOT NULL,
        minutes_full BIGINT NOT NULL DEFAULT 0, minutes_lowest BIGINT NOT NULL DEFAULT 0, received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        PRIMARY KEY (publisher_id, window_start))`)
	loud, quiet := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	at := day.Add(10 * time.Hour)
	e.exec(`INSERT INTO broadcast_usage_hourly (publisher_id, channel_id, hour, viewer_minutes) VALUES ($1, 'c', $3, 9700), ($2, 'c', $3, 10100)`, loud, quiet, at)
	e.exec(`INSERT INTO broadcast_usage_windows (publisher_id, window_start, minutes_full, minutes_lowest) VALUES
        ($1, $3, 9000, 1000), ($2, $3, 10000, 0)`, loud, quiet, at)
	e.exec(`UPDATE broadcast_monitor SET rolled_through = $1`, day.Add(23*time.Hour))

	var logs bytes.Buffer
	jobs := &broadcast.Jobs{DB: e.pool.DB, Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		Now: func() time.Time { return day.Add(26 * time.Hour) }}
	ms, err := jobs.CrossCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Publisher != loud {
		t.Fatalf("mismatches %+v", ms)
	}
	if !strings.Contains(logs.String(), `"code":"broadcast_metering_mismatch"`) || !strings.Contains(logs.String(), `"level":"ERROR"`) ||
		strings.Contains(logs.String(), loud) {
		t.Fatalf("the SEV3 line (hashed ids only): %s", logs.String())
	}
	if again, _ := jobs.CrossCheck(ctx); len(again) != 0 {
		t.Fatal("a day is checked once")
	}
	// before the day's last hour is rolled up, nothing is checked
	e.exec(`UPDATE broadcast_monitor SET checked_day = NULL, rolled_through = $1`, day.Add(20*time.Hour))
	if ms, _ := jobs.CrossCheck(ctx); len(ms) != 0 {
		t.Fatal("checked a day not rolled up")
	}
}

// The rollup catches up hour by hour and stops at the first hour the SQL API can't answer, to
// try it again at the next tick (the edge case: the API down).
func TestBroadcastRollupCatchUp(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 20, 0, 0, time.UTC)
	e.exec(`UPDATE broadcast_monitor SET rolled_through = $1`, now.Truncate(time.Hour).Add(-4*time.Hour))
	down := &broadcast.Jobs{DB: e.pool.DB, AE: &broadcast.SQLClient{}, Logger: slog.Default(), Now: func() time.Time { return now }}
	if n, err := down.CatchUp(ctx); err == nil || n != 0 {
		t.Fatalf("API down: %d, %v", n, err)
	}
	up := &broadcast.Jobs{DB: e.pool.DB, AE: egyptAndGermany("33333333-3333-4333-8333-333333333333"), Logger: slog.Default(), Now: func() time.Time { return now }}
	// 08:00 rolled; 09:00, 10:00 and 11:00 are finished (12:00 is still running)
	if n, err := up.CatchUp(ctx); err != nil || n != 3 {
		t.Fatalf("catch-up: %d, %v", n, err)
	}
	if n, _ := up.CatchUp(ctx); n != 0 {
		t.Fatalf("a second run rolled %d", n)
	}
	// rows past 13 months go
	e.exec(`INSERT INTO broadcast_usage_hourly (publisher_id, channel_id, hour) VALUES ('old', 'c', $1)`, now.AddDate(-14, 0, 0))
	later := &broadcast.Jobs{DB: e.pool.DB, AE: up.AE, Logger: slog.Default(), Now: func() time.Time { return now.Add(time.Hour) }}
	if _, err := later.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM broadcast_usage_hourly WHERE publisher_id = 'old'`); n != 0 {
		t.Fatal("a 14-month-old row was kept")
	}
}
