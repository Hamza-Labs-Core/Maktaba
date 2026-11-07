package queues

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
)

// Story 54.25 T03: a stage with 100 queued and 2 running jobs flips to its plugin and back
// with no job lost or rerun: the running ones end within the grace or are paused (no
// attempt spent), the queued keep their rows and attempts, and the flip is audited.
func TestFlipKeepsEveryJob(t *testing.T) {
	raw, err := os.ReadFile("../../plugins/firstparty/testdata/toy.json")
	if err != nil {
		t.Fatal(err)
	}
	defer firstparty.UseBundle(raw)()
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane) VALUES ('fp_toy','Toy','{library}','cpu','seasons')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO libraries (name, roots) SELECT 'flip ' || g, '{}' FROM generate_series(1, 102) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, library_id, attempts, claimed_by)
		SELECT 'fp_toy', CASE WHEN n <= 2 THEN 'running' ELSE 'pending' END, 'library', id, id, 1,
		       CASE WHEN n <= 2 THEN 'w1' END
		  FROM (SELECT id, row_number() OVER (ORDER BY name) AS n FROM libraries WHERE name LIKE 'flip %') l`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	h := &Handler{DB: db, FlipGrace: 300 * time.Millisecond}
	h.Mount(r)
	rec := extCall(t, r, extAdmin(), "PUT", "/api/queues/fp_toy/impl", map[string]any{"impl": "plugin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var st ImplState
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Impl != "plugin" || st.Plugin == nil || *st.Plugin != "tv.maktaba.toy" || st.Paused != 2 {
		t.Fatalf("answered %+v", st)
	}
	var impl string
	var flipping bool
	_ = db.QueryRow(`SELECT impl, flipping FROM job_stages WHERE stage = 'fp_toy'`).Scan(&impl, &flipping)
	if impl != "plugin" || flipping {
		t.Fatalf("impl %s flipping %v", impl, flipping)
	}
	var pending, paused, attempts, total int
	_ = db.QueryRow(`SELECT count(*) FILTER (WHERE state = 'pending'), count(*) FILTER (WHERE state = 'paused'
		AND paused_reason = 'impl_flip' AND claimed_by IS NULL), sum(attempts), count(*) FROM processing_jobs WHERE stage = 'fp_toy'`).
		Scan(&pending, &paused, &attempts, &total)
	if pending != 100 || paused != 2 || attempts != 102 || total != 102 {
		t.Fatalf("pending %d paused %d attempts %d total %d", pending, paused, attempts, total)
	}
	// idempotent: the same flip again answers the state and changes nothing
	rec = extCall(t, r, extAdmin(), "PUT", "/api/queues/fp_toy/impl", map[string]any{"impl": "plugin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("again: %d", rec.Code)
	}
	rec = extCall(t, r, extAdmin(), "PUT", "/api/queues/fp_toy/impl", map[string]any{"impl": "inproc"})
	if rec.Code != http.StatusOK {
		t.Fatalf("back: %d %s", rec.Code, rec.Body.String())
	}
	var audits int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE category = 'components' AND action = 'stage.impl' AND target_id = 'fp_toy'`).Scan(&audits)
	if audits != 2 {
		t.Fatalf("%d flips audited, want 2", audits)
	}
	// the core's own stages and a bad value are refused
	if rec := extCall(t, r, extAdmin(), "PUT", "/api/queues/probe/impl", map[string]any{"impl": "plugin"}); rec.Code != http.StatusConflict {
		t.Fatalf("a core stage: %d", rec.Code)
	}
	if rec := extCall(t, r, extAdmin(), "PUT", "/api/queues/fp_toy/impl", map[string]any{"impl": "wasm"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad impl: %d", rec.Code)
	}
	// the queue's detail says the stage is a built-in plugin's, and where it runs
	rec = extCall(t, r, extAdmin(), "GET", "/api/queues/fp_toy", nil)
	var d struct {
		Info StageInfo `json:"info"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d.Info.Plugin == nil || *d.Info.Plugin != "tv.maktaba.toy" || d.Info.Impl != "inproc" {
		t.Fatalf("detail %+v", d.Info)
	}
}
