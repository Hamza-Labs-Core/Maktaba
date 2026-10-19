package rules

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type fakeStager struct {
	on      bool
	saved   map[string]map[string]json.RawMessage
	pending map[string]map[string]json.RawMessage
}

func (f *fakeStager) StagesSection(_ context.Context, section string) bool {
	return f.on && (section == "tv" || section == "tv_output")
}

func (f *fakeStager) SaveSettingsDraft(_ context.Context, _ *principal.Principal, section string,
	overrides map[string]json.RawMessage) (string, int64, error) {
	if f.saved == nil {
		f.saved = map[string]map[string]json.RawMessage{}
	}
	f.saved[section] = overrides
	return "c1", 43, nil
}

func (f *fakeStager) PendingSection(_ context.Context, section string) (map[string]json.RawMessage, bool) {
	p, ok := f.pending[section]
	return p, ok
}

// Story 33.12 TC5 / AC8: with staging on a rules.tv save goes into the settings draft
// (the response names it), app_settings is untouched; other sections save directly.
func TestSettingsSaveToDraft(t *testing.T) {
	db := newTestDB(t)
	st := &fakeStager{on: true, pending: map[string]map[string]json.RawMessage{
		"tv": {"horizon_days": json.RawMessage(`5`)}, // an earlier save in the same draft
	}}
	h := &Handler{DB: db, Staging: st}
	r := chi.NewRouter()
	h.Mount(r)
	admin := &principal.Principal{UserID: "u1", IsAdmin: true}

	code, out := do(t, r, http.MethodPatch, "/api/rules/tv", `{"set":{"topup_below_days":2}}`, admin)
	if code != http.StatusOK {
		t.Fatalf("patch tv: %d %v", code, out)
	}
	dc, _ := out["draft_change"].(map[string]any)
	if dc == nil || dc["number"] != float64(43) {
		t.Fatalf("no draft in %v", out)
	}
	if got := st.saved["tv"]; string(got["topup_below_days"]) != "2" || string(got["horizon_days"]) != "5" {
		t.Fatalf("draft got %v (the earlier pending save must be kept)", got)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'rules.tv'`).Scan(&n)
	if n != 0 {
		t.Fatal("rules.tv written to the air")
	}

	// a section a release does not snapshot is written directly
	code, out = do(t, r, http.MethodPatch, "/api/rules/planner", `{"set":{"dawn_quran":false}}`, admin)
	if code != http.StatusOK || out["draft_change"] != nil {
		t.Fatalf("patch planner: %d %v", code, out)
	}
	_ = db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'rules.planner'`).Scan(&n)
	if n != 1 {
		t.Fatal("planner not written")
	}

	// staging off: tv saves directly as before (TC7)
	st.on = false
	code, _ = do(t, r, http.MethodPatch, "/api/rules/tv", `{"set":{"topup_below_days":2}}`, admin)
	_ = db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'rules.tv'`).Scan(&n)
	if code != http.StatusOK || n != 1 {
		t.Fatalf("staging off: %d, rows %d", code, n)
	}
}
