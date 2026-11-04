package queues

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// A component_catchup job carries the component ids of its payload (the Queues card
// says what is catching up); a job of any other stage carries none.
func TestCatchupJobCarriesItsComponents(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`INSERT INTO processing_jobs (subject_kind, subject_id, stage, state, payload)
		VALUES ('catalog', '00000000-0000-0000-0000-00000000ca7a', 'component_catchup', 'pending',
		'{"components":[{"component":"ext.dizquetv","off_since":"2026-11-01T10:00:00Z"},{"component":"livetv","off_since":null}]}'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	refs, err := queryJobRefs(t.Context(), db, jobRefQuery("")+` WHERE j.stage IN ('component_catchup')`)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || len(refs[0].Components) != 2 || refs[0].Components[0] != "ext.dizquetv" || refs[0].Components[1] != "livetv" {
		t.Fatalf("refs %+v", refs)
	}
	// through the stage's detail route, as the card reads it
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	rec := extCall(t, r, extAdmin(), "GET", "/api/queues/component_catchup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var d struct {
		NextUp []map[string]any `json:"next_up"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.NextUp) != 1 {
		t.Fatalf("next_up %v", d.NextUp)
	}
	if c, _ := d.NextUp[0]["components"].([]any); len(c) != 2 || c[0] != "ext.dizquetv" {
		t.Fatalf("components %v", d.NextUp[0]["components"])
	}
	if got := catchupComponents([]byte(`{"components":["youtube"]}`)); len(got) != 1 || got[0] != "youtube" {
		t.Fatalf("plain ids: %v", got)
	}
}
