package components

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.14: GET /api/components carries each machine's health from the status rows,
// and a joined machine not heard from for 90 s is stale.
func TestListCarriesHealth(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	st := New(db)
	if err := st.ReportStatus(ctx, store.Home, "supervisor", []store.Status{{ID: "llm", Phase: "running",
		Version: "9.9.9", Detail: map[string]any{"restarts": 2, "rss_mb": 700,
			"last_error": map[string]any{"code": "crash_loop", "at": "2026-11-03T09:00:00Z"}}}}); err != nil {
		t.Fatal(err)
	}
	quiet := joinNode(t, db, dsn, "quiet")
	if _, err := db.Exec(`UPDATE cluster_nodes SET last_seen_at = now() - interval '5 minutes' WHERE id = $1`, quiet.id); err != nil {
		t.Fatal(err)
	}
	if err := (&store.Store{DB: quiet.db}).ReportStatus(ctx, quiet.id, "pipeline", []store.Status{{ID: "llm", Phase: "running"}}); err != nil {
		t.Fatal(err)
	}
	list, err := NewService(db).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var v *View
	for i := range list.Components {
		if list.Components[i].ID == "llm" {
			v = &list.Components[i]
		}
	}
	if v == nil || v.Health == nil {
		t.Fatalf("llm view %+v", v)
	}
	h := v.Health
	if h.Restarts != 2 || h.Version != "9.9.9" || h.RSSMB == nil || *h.RSSMB != 700 || h.LastErrorCode != "crash_loop" ||
		h.Since == nil || time.Since(*h.Since) > time.Minute || h.Stale {
		t.Fatalf("home health %+v", h)
	}
	if len(v.Machines) != 2 || v.Machines[1].Machine != quiet.id || v.Machines[1].Health == nil || !v.Machines[1].Health.Stale {
		t.Fatalf("machines %+v", v.Machines)
	}
	// the JSON names (epic51-api.md)
	b, _ := json.Marshal(v.Machines[0])
	var raw map[string]map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, k := range []string{"since", "restarts", "last_error_code", "last_error_at", "version", "rss_mb", "stale"} {
		if _, ok := raw["health"][k]; !ok {
			t.Errorf("health has no %q: %s", k, b)
		}
	}
}
