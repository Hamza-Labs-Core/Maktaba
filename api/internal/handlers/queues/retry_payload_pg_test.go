package queues

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// Issue #1763: failures-page retry and queue bulk retry (by cause and by job_ids)
// preserve payload and force_cascade through real PostgreSQL processing_jobs_enqueue
// while stripping one-shot run_now marks.
func TestRetryPreservesPayloadAndForceCascade_PG(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	r := chi.NewRouter()
	h.Mount(r)
	f := &fixture{h: h, r: r}

	const (
		lib  = "61616161-6161-4616-8616-616161616161"
		vid1 = "62626262-6262-4626-8626-626262626261"
		vid2 = "62626262-6262-4626-8626-626262626262"
		vid3 = "62626262-6262-4626-8626-626262626263"
	)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'RetryTest', ARRAY['/media/retry'])`, lib)
	exec(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title)
		VALUES ($1, $4, repeat('1', 64), '/media/retry/a.mp4', 'a.mp4', 1, now(), 'A'),
		       ($2, $4, repeat('2', 64), '/media/retry/b.mp4', 'b.mp4', 1, now(), 'B'),
		       ($3, $4, repeat('3', 64), '/media/retry/c.mp4', 'c.mp4', 1, now(), 'C')`, vid1, vid2, vid3, lib)

	seedFailed := func(vid, stage, cause, payload string, forceCascade bool) int64 {
		t.Helper()
		var jobID int64
		if err := db.QueryRow(`SELECT job_id FROM processing_jobs_enqueue(
			'video', $1::uuid, $2, NULL, true, $3, '{}'::jsonb, NULL, $4::jsonb, NULL)`,
			vid, stage, forceCascade, payload).Scan(&jobID); err != nil {
			t.Fatalf("enqueue %s/%s: %v", vid, stage, err)
		}
		exec(`UPDATE processing_jobs
			SET state = 'failed', attempts = 1, finished_at = now(),
			    error = $2, error_cause = $3, failure_class = 'transient'
			WHERE id = $1`, jobID, `{"kind":"`+cause+`","message":"boom"}`, cause)
		return jobID
	}

	latestPayload := func(vid, stage string) (map[string]any, bool) {
		t.Helper()
		var raw []byte
		var fc bool
		var state string
		if err := db.QueryRow(`SELECT state, payload, force_cascade FROM processing_jobs
			WHERE subject_kind = 'video' AND subject_id = $1::uuid AND stage = $2
			ORDER BY id DESC LIMIT 1`, vid, stage).Scan(&state, &raw, &fc); err != nil {
			t.Fatalf("read latest %s/%s: %v", vid, stage, err)
		}
		if state != "pending" {
			t.Fatalf("latest %s/%s state = %s, want pending", vid, stage, state)
		}
		var p map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("unmarshal payload %q: %v", raw, err)
			}
		}
		return p, fc
	}

	// 1. Failures page retry (retryTargets)
	j1 := seedFailed(vid1, "fingerprint", "nas_timeout",
		`{"payload_seasons":["s1"],"fp_gate":"edgeA","relisten":true,"backfill":true,"run_now":true}`, true)
	rec := f.do("POST", "/api/queues/failures/actions", `{"action":"retry","job_ids":[`+strconv.FormatInt(j1, 10)+`]}`, admin())
	if rec.Code != http.StatusOK {
		t.Fatalf("failures retry: %d %s", rec.Code, rec.Body.String())
	}
	p1, fc1 := latestPayload(vid1, "fingerprint")
	if !fc1 || p1["fp_gate"] != "edgeA" || p1["relisten"] != true || p1["backfill"] != true {
		t.Fatalf("failures retry lost fields: fc=%v payload=%+v", fc1, p1)
	}
	if _, has := p1["run_now"]; has {
		t.Fatalf("failures retry kept run_now: %+v", p1)
	}
	if ps, _ := p1["payload_seasons"].([]any); len(ps) != 1 || ps[0] != "s1" {
		t.Fatalf("failures retry lost payload_seasons: %+v", p1)
	}

	// 2. Queue bulk retry by cause (enqueueTargets)
	_ = seedFailed(vid2, "content_hash", "nas_timeout",
		`{"fp_gate":"edgeB","relisten":true,"run_now":true}`, true)
	rec = f.do("POST", "/api/queues/content_hash/retry", `{"cause":"nas_timeout"}`, admin())
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk retry by cause: %d %s", rec.Code, rec.Body.String())
	}
	p2, fc2 := latestPayload(vid2, "content_hash")
	if !fc2 || p2["fp_gate"] != "edgeB" || p2["relisten"] != true {
		t.Fatalf("bulk retry by cause lost fields: fc=%v payload=%+v", fc2, p2)
	}
	if _, has := p2["run_now"]; has {
		t.Fatalf("bulk retry by cause kept run_now: %+v", p2)
	}

	// 3. Queue bulk retry by job_ids (enqueueTargets)
	j3 := seedFailed(vid3, "content_hash", "file_missing",
		`{"backfill":true,"payload_seasons":["s9"],"run_now":true}`, true)
	rec = f.do("POST", "/api/queues/content_hash/retry", `{"job_ids":[`+strconv.FormatInt(j3, 10)+`]}`, admin())
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk retry by job_ids: %d %s", rec.Code, rec.Body.String())
	}
	p3, fc3 := latestPayload(vid3, "content_hash")
	if !fc3 || p3["backfill"] != true {
		t.Fatalf("bulk retry by job_ids lost fields: fc=%v payload=%+v", fc3, p3)
	}
	if _, has := p3["run_now"]; has {
		t.Fatalf("bulk retry by job_ids kept run_now: %+v", p3)
	}
	if ps, _ := p3["payload_seasons"].([]any); len(ps) != 1 || ps[0] != "s9" {
		t.Fatalf("bulk retry by job_ids lost payload_seasons: %+v", p3)
	}
}
