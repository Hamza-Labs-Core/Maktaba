//go:build integration

package queues

// Epic 40 against a real, migrated Postgres: the tile health of the
// snapshot, the failures list with its facets, and the actions (the
// scripted driver of the unit tests cannot tell whether the SQL is valid).

import (
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

func TestQueueHealth_AgainstPostgres(t *testing.T) {
	db := parityDB(t)
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	r := chi.NewRouter()
	h.Mount(r)
	f := &fixture{h: h, r: r}

	const lib = "51515151-5151-4515-8515-515151515151"
	const vid = "52525252-5252-4525-8525-525252525252"
	const vid2 = "53535353-5353-4535-8535-535353535353"
	mustExec(t, db, `INSERT INTO libraries (id, name, roots) VALUES ($1, 'Lessons', ARRAY['smb://nas/O'])`, lib)
	mustExec(t, db, `INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title)
		VALUES ($1, $2, repeat('c', 64), 'smb://nas/O/a_1.mp4', 'a_1.mp4', 1, now(), 'Lesson 1'),
		       ($3, $2, repeat('d', 64), 'smb://nas/O/b.mp4', 'b.mp4', 1, now(), 'Lesson 2')`, vid, lib, vid2)
	enqueue := func(id, stage string) int64 {
		var job int64
		if err := db.QueryRow(`SELECT job_id FROM processing_jobs_enqueue('video', $1::uuid, $2, NULL, false, false,
			'{}'::jsonb, NULL, NULL, NULL)`, id, stage).Scan(&job); err != nil {
			t.Fatalf("enqueue %s: %v", stage, err)
		}
		return job
	}
	failed := enqueue(vid, "content_hash")
	mustExec(t, db, `UPDATE processing_jobs SET state = 'failed', attempts = 1, finished_at = now(),
		error = '{"kind":"file_missing","message":"a_1.mp4: not found"}', error_cause = 'file_missing',
		failure_class = 'item_permanent' WHERE id = $1`, failed)
	parked := enqueue(vid2, "fingerprint")
	mustExec(t, db, `UPDATE processing_jobs SET parked_on = 'dependency:share:smb://nas/O',
		failure_class = 'dependency:share:smb://nas/O', not_before = now() + interval '1 day',
		error = '{"kind":"file_missing","message":"b.mp4: not found"}', error_cause = 'file_missing' WHERE id = $1`, parked)
	mustExec(t, db, `INSERT INTO job_events (job_id, subject_kind, subject_id, library_id, stage, level, kind, message, data)
		VALUES ($1, 'video', $2, $3, 'content_hash', 'error', 'failed', 'file_missing: not found',
		        '{"cause":"file_missing","class":"item_permanent"}')`, failed, vid, lib)
	mustExec(t, db, `INSERT INTO dependency_health (name, healthy, message) VALUES ('dependency:share:smb://nas/O', false, 'offline')`)

	rec := f.do("GET", "/api/queues", "", admin())
	if rec.Code != 200 {
		t.Fatalf("snapshot %d %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	decode(t, rec, &snap)
	for _, q := range snap.Queues {
		switch q.Stage {
		case "content_hash":
			if q.Health == nil || q.Health.State != HealthOK || q.Health.NeedsYou != 1 || q.Health.FailedWindow != 1 {
				t.Fatalf("content_hash health %+v", q.Health)
			}
		case "fingerprint":
			if q.Health == nil || q.Health.State != HealthWaiting || q.Health.Waiting[0].Message == nil {
				t.Fatalf("fingerprint health %+v", q.Health)
			}
		}
	}

	type list struct {
		Items  []FailureItem           `json:"items"`
		Facets map[string][]FacetCount `json:"facets"`
	}
	get := func(q string) list {
		t.Helper()
		rec := f.do("GET", "/api/queues/failures"+q, "", admin())
		if rec.Code != 200 {
			t.Fatalf("failures%s %d %s", q, rec.Code, rec.Body.String())
		}
		var l list
		decode(t, rec, &l)
		return l
	}
	all := get("")
	if len(all.Items) != 2 || len(all.Facets["stage"]) != 2 {
		t.Fatalf("all %+v", all)
	}
	if got := get("?status=waiting&library=" + lib); len(got.Items) != 1 || got.Items[0].Reason != "waiting:dependency:share:smb://nas/O" {
		t.Fatalf("waiting %+v", got.Items)
	}
	if got := get("?path_prefix=smb://nas/O/a_&class=item_permanent"); len(got.Items) != 1 || got.Items[0].JobID != failed {
		t.Fatalf("prefix + class %+v", got.Items)
	}
	if got := get("?class=dependency"); len(got.Items) != 1 || got.Items[0].JobID != parked {
		t.Fatalf("class prefix %+v", got.Items)
	}

	post := func(body string) FixResult {
		t.Helper()
		rec := f.do("POST", "/api/queues/failures/actions", body, admin())
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
		var res FixResult
		decode(t, rec, &res)
		return res
	}
	if res := post(`{"action":"ignore","filter":{"stage":"content_hash"}}`); res.Affected != 1 {
		t.Fatalf("ignore %+v", res)
	}
	if got := get(""); len(got.Items) != 1 {
		t.Fatalf("an ignored item is still listed: %+v", got.Items)
	}
	if got := get("?status=ignored"); len(got.Items) != 1 {
		t.Fatalf("ignored filter %+v", got.Items)
	}
	if res := post(`{"action":"unignore","filter":{"status":"ignored"}}`); res.Affected != 1 {
		t.Fatalf("unignore %+v", res)
	}
	if res := post(`{"action":"retry","filter":{}}`); res.Affected != 2 {
		t.Fatalf("retry %+v", res)
	}
	var parkedOn *string
	if err := db.QueryRow(`SELECT parked_on FROM processing_jobs WHERE id = $1`, parked).Scan(&parkedOn); err != nil || parkedOn != nil {
		t.Fatalf("released? %v %v", parkedOn, err)
	}
	if got := get(""); len(got.Items) != 1 || got.Items[0].Status != StatusRetrying {
		t.Fatalf("after retry %+v", got.Items)
	}
	mustExec(t, db, `INSERT INTO instance_identity (id, name) VALUES (1, 'home') ON CONFLICT (id) DO NOTHING`)
	post(`{"action":"path_map","job_ids":[` + itoa(int(parked)) + `],"path_map":{"from":"smb://nas/O/","to":"/Volumes/O/"}}`)
	var pm string
	if err := db.QueryRow(`SELECT path_map->>'smb://nas/O/' FROM instance_identity WHERE id = 1`).Scan(&pm); err != nil || pm != "/Volumes/O/" {
		t.Fatalf("path map %q %v", pm, err)
	}
	var audits int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE action LIKE 'queue.failures.%'`).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("audits %d %v", audits, err)
	}
}
