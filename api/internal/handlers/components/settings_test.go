package components

import (
	"database/sql"
	"encoding/json"
	"testing"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

func (f *fixture) putSetting(t *testing.T, key, value string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)`, key, value); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) setting(t *testing.T, key string) (string, bool) {
	t.Helper()
	var v string
	err := f.db.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, true
}

// Story 51.13: disabling, stopping and removing a component keep its settings; turned
// back on, every changed setting is there.
func TestSettingsAreKeptOnDisableAndRemove(t *testing.T) {
	f := newFixture(t)
	f.putSetting(t, "rules.transcription", `{"relisten_tries":9}`)
	f.putSetting(t, "rules_version.transcription", `{"major":1,"minor":0}`)
	for _, step := range []struct{ method, path, body string }{
		{"PUT", "/api/components/transcription", `{"want":"installed"}`},
		{"POST", "/api/components/transcription/stop", ""},
		{"POST", "/api/components/transcription/start", ""},
		{"PUT", "/api/components/subtitles", `{"want":"absent"}`},
		{"PUT", "/api/components/transcription", `{"want":"absent"}`},
		{"PUT", "/api/components/transcription", `{"want":"enabled"}`},
	} {
		rec := f.do(step.method, step.path, step.body, admin())
		if rec.Code/100 != 2 {
			t.Fatalf("%s %s: %d %s", step.method, step.path, rec.Code, rec.Body)
		}
	}
	if v, ok := f.setting(t, "rules.transcription"); !ok || v != `{"relisten_tries":9}` {
		t.Fatalf("settings after disable, remove and enable: %q %v", v, ok)
	}
	if _, ok := f.setting(t, "rules_version.transcription"); !ok {
		t.Fatal("the version key went")
	}
}

// T11: a reset deletes both keys of every section the component owns, one audit row per
// section with the old value in before; other components' settings stay.
func TestResetSettings(t *testing.T) {
	f := newFixture(t)
	f.putSetting(t, "rules.transcription", `{"relisten_tries":9,"relisten_round":2}`)
	f.putSetting(t, "rules_version.transcription", `{"major":1,"minor":0,"failed":"boom"}`)
	f.putSetting(t, "rules.planner", `{"dawn_quran":false}`)

	var res svc.SettingsReset
	decode(t, f.do("POST", "/api/components/transcription/settings/reset", "", admin()), 200, &res)
	if res.ID != "transcription" || res.Changed != 2 || len(res.Sections) != 1 || res.Sections[0].Section != "transcription" {
		t.Fatalf("answer %+v", res)
	}
	for _, k := range []string{"rules.transcription", "rules_version.transcription"} {
		if _, ok := f.setting(t, k); ok {
			t.Errorf("%s still there", k)
		}
	}
	if _, ok := f.setting(t, "rules.planner"); !ok {
		t.Error("another component's settings went")
	}
	var action, target, payload string
	if err := f.db.QueryRow(`SELECT action, target_id, payload FROM audit_log WHERE category = 'components'
		ORDER BY id DESC LIMIT 1`).Scan(&action, &target, &payload); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Before struct {
			Value   map[string]any `json:"value"`
			Version map[string]any `json:"version"`
		} `json:"before"`
		After any `json:"after"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if action != svc.AuditSettingsReset || target != "transcription" || p.Before.Value["relisten_tries"] != float64(9) ||
		p.Before.Version["failed"] != "boom" || p.After != nil {
		t.Fatalf("audit %s %s %s", action, target, payload)
	}
	// nothing left: a second reset writes no audit row
	n := f.audits(t)
	decode(t, f.do("POST", "/api/components/transcription/settings/reset", "", admin()), 200, &res)
	if f.audits(t) != n || res.Changed != 0 {
		t.Fatalf("a reset of nothing wrote: %d -> %d", n, f.audits(t))
	}
	code(t, f.do("POST", "/api/components/nope/settings/reset", "", admin()), 404, svc.CodeUnknown)
}

// Remove with purge takes the component off and resets its settings; a refused remove
// keeps them.
func TestPurgeResetsTheSettings(t *testing.T) {
	f := newFixture(t)
	f.putSetting(t, "rules.exactdates", `{"enabled":true}`)
	decode(t, f.do("DELETE", "/api/components/youtube?purge=true", "", admin()), 200, nil)
	if f.want(t, "youtube").Want != components.WantAbsent {
		t.Fatal("youtube not removed")
	}
	if _, ok := f.setting(t, "rules.exactdates"); ok {
		t.Fatal("purge kept the settings")
	}

	f.putSetting(t, "rules.transcription", `{"relisten_tries":9}`)
	code(t, f.do("DELETE", "/api/components/transcription?purge=true", "", admin()), 409, svc.CodeRequiredBy)
	if _, ok := f.setting(t, "rules.transcription"); !ok {
		t.Fatal("a refused purge reset the settings")
	}
}
