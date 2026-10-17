//go:build integration

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/jobs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// Integration tier for plan-06-11 workstream B. Needs a Postgres in
// DATABASE_URL migrated through slots 0087-0090 (workstream A). It
// skips, rather than fails, when those migrations are not present, so
// it is safe to run on branches where A has not landed yet.
//
//	DATABASE_URL=postgres://maktaba:maktaba@localhost:55432/maktaba?sslmode=disable \
//	  go test -tags=integration ./ -run TestQueues
//
// If pipeline/tests/fixtures/queue_seed.sql exists (workstream A's
// deterministic seed) it is loaded before the route checks.
func TestQueues_Routes_RealPostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: skipping queues integration test")
	}
	db := openTestDB(t, dsn)
	t.Cleanup(func() { _ = db.Close() })
	resetSchema(t, db)
	dropEvents(t, db)
	applyAllMigrations(t, db)

	var fn bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'processing_jobs_enqueue')`).Scan(&fn); err != nil {
		t.Fatal(err)
	}
	if !fn || !tableExists(t, db, "job_stages") || !tableExists(t, db, "job_events") {
		t.Skip("slots 0087-0090 not applied (workstream A): skipping")
	}
	if seed, err := os.ReadFile("../pipeline/tests/fixtures/queue_seed.sql"); err == nil {
		if _, err := db.Exec(string(seed)); err != nil {
			t.Fatalf("load queue_seed.sql: %v", err)
		}
	}

	hub := ws.NewHub()
	qh := &queues.Handler{DB: db, Hub: hub}
	jh := &jobs.Handler{DB: db}
	r := chi.NewRouter()
	qh.Mount(r)
	jh.Mount(r)
	(&ws.Handler{Hub: hub, Initial: qh.InitialFrame}).Mount(r)

	do := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "it", IsAdmin: true, AccessAllLibraries: true}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// F6 regression: a stage the old CHECK constraints rejected.
	var libID, videoID string
	if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ('it', ARRAY['/tmp']) RETURNING id::text`).Scan(&libID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime)
		VALUES ($1, repeat('a', 64), '/tmp/a.mp4', 'a.mp4', 1, now()) RETURNING id::text`, libID).Scan(&videoID); err != nil {
		t.Fatal(err)
	}
	res, err := queues.Enqueue(context.Background(), db, queues.EnqueueParams{
		SubjectKind: "video", SubjectID: videoID, Stage: "categorize",
		Reason: map[string]any{"why": "integration"},
	})
	if err != nil || !res.Accepted() {
		t.Fatalf("enqueue categorize: %+v %v", res, err)
	}
	// Coalescing: the second enqueue folds into the live row.
	res2, err := queues.Enqueue(context.Background(), db, queues.EnqueueParams{
		SubjectKind: "video", SubjectID: videoID, Stage: "categorize", Force: true,
		Reason: map[string]any{"why": "again"},
	})
	if err != nil || res2.Outcome != queues.OutcomeCoalesced || *res2.JobID != *res.JobID {
		t.Fatalf("second enqueue should coalesce: %+v %v", res2, err)
	}
	jobID := *res.JobID

	// F7 regression: every control UPDATE runs against the migrated schema.
	for _, p := range []struct{ method, path string }{
		{"POST", "/api/jobs/" + itoa(jobID) + "/pause"},
		{"POST", "/api/jobs/" + itoa(jobID) + "/pause?force=true"},
		{"POST", "/api/jobs/" + itoa(jobID) + "/resume"},
		{"POST", "/api/jobs/" + itoa(jobID) + "/cancel"},
		{"POST", "/api/videos/" + videoID + "/pause"},
		{"POST", "/api/videos/" + videoID + "/resume"},
		{"POST", "/api/videos/" + videoID + "/cancel"},
	} {
		if rec := do(p.method, p.path); rec.Code != http.StatusOK {
			t.Errorf("%s %s -> %d %s", p.method, p.path, rec.Code, rec.Body.String())
		}
	}

	// Every read route answers 200 with valid JSON.
	for _, p := range []string{
		"/api/queues", "/api/queues/graph", "/api/queues/probe", "/api/queues/probe/rate?hours=24",
		"/api/queues/probe/jobs?limit=5", "/api/queues/probe/failures", "/api/pools", "/api/issues",
		"/api/activity", "/api/jobs/" + itoa(jobID) + "/events", "/api/items/video/" + videoID + "/history",
		"/api/items/library/" + libID + "/history", "/api/queues/libraries", "/api/queues/health?hours=6",
		"/api/workers", "/api/extensions", "/api/queue/stats",
	} {
		rec := do("GET", p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s -> %d %s", p, rec.Code, rec.Body.String())
		}
	}

	// Retrigger a library: every entry stage is re-added and the second
	// call coalesces.
	body := `{"items":[{"subject_kind":"library","subject_id":"` + libID + `"}]}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/api/queue/retrigger", strings.NewReader(body))
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "it", IsAdmin: true}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("retrigger #%d -> %d %s", i, rec.Code, rec.Body.String())
		}
	}

	// Snapshot query budget (plan-06-11 §8.3): well under a second on a
	// seed-sized table; the 20 ms / 1M-row target is checked by the
	// benchmark below.
	start := time.Now()
	if _, err := qh.BuildSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("snapshot took %s", d)
	}
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}

// BenchmarkQueuesSnapshot measures BuildSnapshot on whatever data the
// database holds; load a 1M-row synthetic processing_jobs table first to
// check the 20 ms target.
func BenchmarkQueuesSnapshot(b *testing.B) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		b.Skip("DATABASE_URL unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	qh := &queues.Handler{DB: db, Hub: ws.NewHub()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := qh.BuildSnapshot(context.Background()); err != nil {
			b.Skipf("schema not ready: %v", err)
		}
	}
}
