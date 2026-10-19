package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/transcription"
)

// Postgres only (they skip without MAKTABA_TEST_PG_URL).

// sample is one round of a process's health numbers, as the pipeline's
// sample_once measured it.
type sample struct {
	at   time.Time
	p    agent.Proc
	vals map[string]float64
}

// workload is 12 minutes of three processes (two on one host, two of
// them transcribing, one losing audio once), every 10 s.
func workload(t0 time.Time) []sample {
	a := agent.Proc{WorkerID: "studio/101/aaaa", Host: "studio", IP: "192.168.50.20"}
	b := agent.Proc{WorkerID: "studio/102/bbbb", Host: "studio", IP: "192.168.50.20"}
	c := agent.Proc{WorkerID: "box/7/cccc", Host: "linux-box", IP: "192.168.50.31"}
	var out []sample
	for k := 0; k < 72; k++ {
		base := t0.Add(time.Duration(k*10) * time.Second)
		common := func(off float64) map[string]float64 {
			m := map[string]float64{
				"nas.open_ms_p50": 3 + off + float64(k%4), "nas.open_ms_p95": 30 + off + float64(k%9),
				"nas.timeouts": float64(map[bool]int{true: 2}[k%7 == 0]), "cpu.pct": 20 + off + float64(k%13),
				"mem.free_pct": 40 + off, "mem.available_gb": 27.5 - off,
			}
			if k%6 == 0 {
				m["net.plex_ms"] = 12 + off
			}
			return m
		}
		va := common(0)
		va["stt.audio_sec:stt-a"] = float64(30 * k)
		va["stt.files_in_flight:stt-a"] = float64(k % 3)
		out = append(out, sample{base, a, va})
		vb := common(1)
		vb["stt.audio_sec:stt-b"] = float64(25 * k)
		vb["stt.audio_lost:stt-b"] = float64(map[bool]int{true: 120}[k >= 40])
		out = append(out, sample{base.Add(3 * time.Second), b, vb})
		out = append(out, sample{base.Add(5 * time.Second), c, common(2)})
	}
	return out
}

// readersView is what the health and transcription readers answer.
func readersView(t *testing.T, db *sql.DB, now time.Time) string {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return now }
	sum, err := (&transcription.Handler{DB: db, NowFunc: clock}).Build(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	qh := &queues.Handler{DB: db, NowFunc: clock}
	snap, err := qh.BuildSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	qh.Mount(r)
	get := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u1"}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	var stage string
	_ = db.QueryRow(`SELECT stage FROM job_stages ORDER BY stage LIMIT 1`).Scan(&stage)
	parts := []string{mustJSON(t, sum.Rate), mustJSON(t, sum.ETA), mustJSON(t, snap.Health), get("/api/queues/health?hours=1")}
	if stage != "" {
		parts = append(parts, get("/api/queues/"+stage+"/rate?hours=1"))
	}
	return strings.Join(parts, "\n")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Fed the health numbers through the cache, today's readers — the
// transcription report's live speed and time left, the Processing page's
// health strip and sparklines, GET /api/queues/health, a queue's rate
// chart — answer what they answered when each worker wrote its own rows
// (engine/health.py sample_once).
func TestPGReadersSeeWhatTheWorkersWrote(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	t0 := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	now := t0.Add(12 * time.Minute)
	samples := workload(t0)

	// before: every worker wrote its round itself, nas.reads_busy included
	var busy float64
	if err := db.QueryRow(readsBusySQL).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		for m, v := range s.vals {
			if _, err := db.Exec(`INSERT INTO health_samples (at, host, metric, value) VALUES ($1,$2,$3,$4)
ON CONFLICT (metric, host, at) DO UPDATE SET value = EXCLUDED.value`, s.at, s.p.Host, m, v); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO health_samples (at, host, metric, value) VALUES ($1,$2,'nas.reads_busy',$3)
ON CONFLICT (metric, host, at) DO UPDATE SET value = EXCLUDED.value`, s.at, s.p.Host, busy); err != nil {
			t.Fatal(err)
		}
	}
	old := readersView(t, db, now)
	if !strings.Contains(old, `"workers":2`) {
		t.Fatalf("the workload shows no live speed: %s", old)
	}
	must2(t, db, `TRUNCATE health_samples`)

	// after: the processes send them; the hub writes them
	clk := &fakeClock{t: t0}
	c := newTestCache(db, "postgres", clk, 24*time.Hour)
	for _, s := range samples {
		clk.Set(s.at)
		c.Metrics(s.p, nil, s.vals, false)
		c.flushMetrics(bg)
	}
	if got := readersView(t, db, now); got != old {
		t.Fatalf("the readers' answers changed:\nworkers wrote:\n%s\nhub wrote:\n%s", old, got)
	}
}

// The machines' group may read health_samples but no longer write it, and
// has no right at all on the cache tables; the cache tables are UNLOGGED,
// health_samples is not.
func TestPGWorkerRoleAndPersistence(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	var group bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')`).Scan(&group); err != nil || !group {
		t.Skipf("no maktaba_worker role (%v)", err)
	}
	for table, want := range map[string]string{
		"cluster_metrics_cache": "u", "cluster_log_cache": "u", "cluster_cache_files": "u", "health_samples": "p",
	} {
		var p string
		if err := db.QueryRow(`SELECT relpersistence FROM pg_class WHERE relname = $1 AND relkind = 'r'`, table).Scan(&p); err != nil || p != want {
			t.Errorf("%s persistence = %q, want %q (%v)", table, p, want, err)
		}
	}

	role := "t_c1_worker_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	pw := uuid.NewString()
	must2(t, db, `CREATE ROLE `+pq.QuoteIdentifier(role)+` LOGIN PASSWORD `+pq.QuoteLiteral(pw)+` IN ROLE maktaba_worker`)
	t.Cleanup(func() {
		_, _ = db.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role)
		_, _ = db.Exec(`DROP ROLE IF EXISTS ` + pq.QuoteIdentifier(role))
	})
	w, err := sql.Open("postgres", clustertest.WithUser(t, dsn, role, pw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(`SELECT count(*) FROM health_samples`); err != nil {
		t.Fatalf("a worker must still read health_samples: %v", err)
	}
	if _, err := w.Exec(`SELECT 1 FROM job_stages LIMIT 1`); err != nil {
		t.Fatalf("a worker reads the queue tables: %v", err)
	}
	denied := []string{
		`INSERT INTO health_samples (at, host, metric, value) VALUES (now(), 'node', 'cpu.pct', 1)`,
		`UPDATE health_samples SET value = 0`,
		`DELETE FROM health_samples`,
	}
	for _, table := range []string{"cluster_metrics_cache", "cluster_log_cache", "cluster_cache_files"} {
		denied = append(denied, `SELECT count(*) FROM `+table, `DELETE FROM `+table)
	}
	denied = append(denied,
		`INSERT INTO cluster_log_cache (worker_id, ts, h, ip, level, rank, event, logger, fields)
VALUES ('w', now(), 1, '10.0.0.1', 'info', 20, 'e', 'l', '{}')`,
		`INSERT INTO cluster_metrics_cache (worker_id, slot, at, ip, frame) VALUES ('w', now(), now(), '10.0.0.1', '{}')`,
		`INSERT INTO cluster_cache_files (kind, ip, day, worker, root, read_to) VALUES ('logs', 'x', 'd', 'w', '/', 0)`)
	for _, q := range denied {
		_, err := w.Exec(q)
		var pqe *pq.Error
		if !errors.As(err, &pqe) || pqe.Code != "42501" {
			t.Errorf("%s: %v, want permission denied", q[:min(60, len(q))], err)
		}
	}
}
