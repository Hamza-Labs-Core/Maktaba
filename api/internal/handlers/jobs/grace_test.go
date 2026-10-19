package jobs

import (
	"database/sql/driver"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg/runtimecfgtest"
)

// pause.grace_sec: a force pause waits for the requested pause's grace; unset,
// a force pause is taken at once (the behaviour before the setting).
func TestForcePauseGrace(t *testing.T) {
	forced := func(db *queuestest.DB) bool {
		for _, c := range db.Calls() {
			if c.Exec && strings.Contains(c.SQL, "paused_reason='user-force'") {
				return true
			}
		}
		return false
	}
	setup := func(grace string, requested bool, askedAgo time.Duration) (*queuestest.DB, chi.Router) {
		db := queuestest.New(
			&queuestest.Rule{Match: "select pause_requested, updated_at from processing_jobs", Cols: []string{"pause_requested", "updated_at"},
				Rows: [][]driver.Value{{requested, now0.Add(-askedAgo)}}},
			&queuestest.Rule{Match: "from processing_jobs where id = $1", Cols: jobCols,
				Rows: [][]driver.Value{jobRow(9, "transcribe", "running")}})
		h := &Handler{DB: db.DB, NowFunc: func() time.Time { return now0 }}
		if grace != "" {
			sdb := runtimecfgtest.DB(t)
			runtimecfgtest.Set(t, sdb, "pause.grace_sec", grace)
			h.Settings = runtimecfg.New(sdb)
		}
		r := chi.NewRouter()
		h.Mount(r)
		return db, r
	}
	t.Run("unset: at once", func(t *testing.T) {
		db, r := setup("", false, 0)
		if rec := call(r, "POST", "/api/jobs/9/pause?force=true", true); rec.Code != 200 || !forced(db) {
			t.Fatalf("status %d forced %v", rec.Code, forced(db))
		}
	})
	t.Run("inside the grace: refused", func(t *testing.T) {
		db, r := setup("60", true, 20*time.Second)
		rec := call(r, "POST", "/api/jobs/9/pause?force=true", true)
		if rec.Code != http.StatusConflict || forced(db) || !strings.Contains(rec.Body.String(), "40 s") {
			t.Fatalf("status %d forced %v %s", rec.Code, forced(db), rec.Body.String())
		}
	})
	t.Run("no pause requested: refused", func(t *testing.T) {
		db, r := setup("60", false, time.Hour)
		if rec := call(r, "POST", "/api/jobs/9/pause?force=true", true); rec.Code != http.StatusConflict || forced(db) {
			t.Fatalf("status %d forced %v", rec.Code, forced(db))
		}
	})
	t.Run("after the grace: forced", func(t *testing.T) {
		db, r := setup("60", true, 61*time.Second)
		if rec := call(r, "POST", "/api/jobs/9/pause?force=true", true); rec.Code != 200 || !forced(db) {
			t.Fatalf("status %d forced %v", rec.Code, forced(db))
		}
	})
}
