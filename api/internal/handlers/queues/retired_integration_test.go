//go:build integration

package queues

// The rows of the stages a release removed stay (slot 0215 drops no data: a release's
// migrations lose none) until a later cleanup migration. With a plex_groups stage row and
// its jobs (one waiting, one failed, one claimed by a worker long gone) and the network_tv
// pool present, every queue read answers as before and none of them names the old stage
// or pool: no tile, failure group, waiting count, stuck job or pool comes of them.
//
//	MAKTABA_TEST_PG_URL=postgres://u:p@127.0.0.1:5432/postgres?sslmode=disable \
//	    go test -tags integration ./internal/handlers/queues/ -run Retired

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

func TestRetiredStageRows_AreIgnoredByEveryRead(t *testing.T) {
	db := parityDB(t)
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	r := chi.NewRouter()
	h.Mount(r)
	f := &fixture{h: h, r: r}

	const lib, lib2 = "21500000-0000-4000-8000-00000000000a", "21500000-0000-4000-8000-00000000000b"
	mustExec(t, db, `DELETE FROM processing_jobs`)
	mustExec(t, db, `INSERT INTO libraries (id, name, roots) VALUES ($1, 'قناة', ARRAY['/x']), ($2, 'أخرى', ARRAY['/y'])`, lib, lib2)
	// the release before wrote the row with its graph sync (a copy of a seeded stage's row,
	// renamed), enabled, last synced long ago: a live stage like it reads as stale_graph
	mustExec(t, db, `INSERT INTO job_stages
		SELECT (jsonb_populate_record(NULL::job_stages, to_jsonb(s) || jsonb_build_object(
		        'stage', 'plex_groups', 'label', 'Plex groups', 'pool', 'network_plex',
		        'subject_kinds', ARRAY['library'], 'enabled', true, 'disabled_reason', NULL,
		        'paused', false, 'external_write', true, 'extension', 'plex',
		        'graph_synced_at', now() - interval '30 days'))).*
		  FROM job_stages s WHERE s.stage = 'probe'`)
	mustExec(t, db, `INSERT INTO worker_pools (pool, label, scope, size, default_size)
		VALUES ('network_tv', 'dizqueTV', 'global', 1, 1) ON CONFLICT (pool) DO NOTHING`)
	mustExec(t, db, `INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage, state, created_at)
		VALUES ('library', $1, $1, 'plex_groups', 'pending', now() - interval '2 hours')`, lib)
	mustExec(t, db, `INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage, state, error_cause, error, finished_at)
		VALUES ('library', $1, $1, 'plex_groups', 'failed', 'service_unreachable', 'plex down', now() - interval '3 days')`, lib)
	mustExec(t, db, `INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage, state, claimed_by, claimed_at, last_heartbeat_at)
		VALUES ('library', $1, $1, 'plex_groups', 'claimed', 'old-worker', now() - interval '1 day', now() - interval '1 day')`, lib2)

	for _, path := range []string{
		"/api/queues", "/api/queues/graph", "/api/queues/health", "/api/queues/pulse",
		"/api/queues/running", "/api/queues/failures", "/api/queues/libraries",
		"/api/queues/libraries/activity", "/api/pools", "/api/queues/insights",
	} {
		rec := f.do("GET", path, "", admin())
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, gone := range []string{"plex_groups", "network_tv"} {
			if strings.Contains(body, `"`+gone+`"`) {
				t.Errorf("%s names %s: %s", path, gone, body)
			}
		}
	}

	// the old stage reads as no stage at all
	if rec := f.do("GET", "/api/queues/plex_groups", "", admin()); rec.Code != http.StatusNotFound {
		t.Errorf("detail of the old stage: %d %s", rec.Code, rec.Body.String())
	}

	// its waiting and claimed jobs are neither work waiting nor a stuck worker
	var pulse Pulse
	rec := f.do("GET", "/api/queues/pulse", "", admin())
	decode(t, rec, &pulse)
	if pulse.Waiting != 0 || pulse.Running != 0 || pulse.StuckCount != 0 || pulse.State != PulseIdle {
		t.Errorf("pulse %+v", pulse)
	}

	// the rows themselves are kept as they were
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM processing_jobs WHERE stage = 'plex_groups'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("old jobs %d %v", n, err)
	}
}
