package flags

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/serverkeys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	flagspkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/flags"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// pgHome is the flag surface over a scratch Postgres migrated to the
// current slots, with an admin, a viewer and a second user.
type pgHome struct {
	t     *testing.T
	db    *sql.DB
	h     *Handler
	ent   *fixedEnt
	other string
	do    func(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder
}

func homeLicense() *subscriptions.Entitlements {
	return &subscriptions.Entitlements{Tier: subscriptions.TierHome, Features: map[subscriptions.Feature]bool{
		subscriptions.FeatureCloudRelay: true, subscriptions.FeatureMultiUser: true,
		subscriptions.FeatureCloudBackup: true, subscriptions.FeatureAdvancedMetric: true,
	}}
}

func newPGHome(t *testing.T) *pgHome {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	key, err := serverkeys.Generate(time.Now(), "generated")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := serverkeys.NewStore(key, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ent := &fixedEnt{e: homeLicense()}
	h := New(db, ent, keys, nil)
	ph := &pgHome{t: t, db: db, h: h, ent: ent, other: uuid.NewString(), do: serve(h)}
	ph.addUser(adminID, "flags-admin", true)
	ph.addUser(viewerID, "flags-viewer", false)
	ph.addUser(ph.other, "flags-other", false)
	return ph
}

func (p *pgHome) addUser(id, name string, admin bool) {
	p.t.Helper()
	if _, err := p.db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, $2, 'x', $3)`, id, name, admin); err != nil {
		p.t.Fatal(err)
	}
}

func (p *pgHome) bundle(who *principal.Principal) flagspkg.Bundle {
	p.t.Helper()
	rec := p.do(http.MethodGet, "/api/me/flags", nil, who)
	if rec.Code != http.StatusOK {
		p.t.Fatalf("GET /api/me/flags: %d %s", rec.Code, rec.Body)
	}
	var b flagspkg.Bundle
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		p.t.Fatal(err)
	}
	if err := flagspkg.Verify(&b, p.h.Resolver.Keys, time.Now()); err != nil {
		p.t.Fatalf("the bundle must verify with the server's key: %v", err)
	}
	return b
}

func (p *pgHome) want(rec *httptest.ResponseRecorder, code int) map[string]any {
	p.t.Helper()
	if rec.Code != code {
		p.t.Fatalf("status %d, want %d: %s", rec.Code, code, rec.Body)
	}
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return out
}

func (p *pgHome) auditCount(action string) int {
	p.t.Helper()
	var n int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category = 'flags' AND action = $1`, action).Scan(&n); err != nil {
		p.t.Fatal(err)
	}
	return n
}

func TestMeFlags_HomeTierSignedWithKid(t *testing.T) {
	p := newPGHome(t)
	b := p.bundle(viewerP())
	if b.Tier != "home" || b.Kid != p.h.Resolver.Keys.Active().Kid {
		t.Fatalf("tier %q kid %q", b.Tier, b.Kid)
	}
	lic := p.ent.e
	for _, d := range flagspkg.Declarations {
		if d.Feature != "" && b.Flags[d.Key] != lic.Allows(d.Feature) {
			t.Errorf("%s = %v, want Entitlements.Allows = %v", d.Key, b.Flags[d.Key], lic.Allows(d.Feature))
		}
	}
	issued, _ := time.Parse(time.RFC3339, b.IssuedAt)
	expires, _ := time.Parse(time.RFC3339, b.ExpiresAt)
	if expires.Sub(issued) != 60*time.Second {
		t.Errorf("bundle lifetime %v, want 60s", expires.Sub(issued))
	}
	if _, ok := b.Flags[flagspkg.FlagFlagsAdmin]; ok {
		t.Error("a viewer must not see the admin-only flag")
	}
	if p.bundle(adminP()).Flags[flagspkg.FlagFlagsAdmin] != true {
		t.Error("an admin sees the admin-only flag")
	}
	if p.bundle(playP()).Flags[flagspkg.FlagFlagsAdmin] != nil {
		t.Error("a play-scoped cloud sign-in is no admin")
	}
	rec := p.do(http.MethodGet, "/api/me/flags/keys", nil, viewerP())
	if got := p.want(rec, http.StatusOK)["active"].(map[string]any)["kid"]; got != b.Kid {
		t.Errorf("keys endpoint kid %v, want %s", got, b.Kid)
	}
}

func TestLicenseChangeShowsAtOnce(t *testing.T) {
	p := newPGHome(t)
	if p.bundle(viewerP()).Flags[flagspkg.FlagCloudRelay] != true {
		t.Fatal("home tier has cloud_relay")
	}
	p.ent.e = nil // the license lapsed: the store now answers free
	b := p.bundle(viewerP())
	if b.Flags[flagspkg.FlagCloudRelay] != false || b.Tier != "free" {
		t.Errorf("after the lapse: tier %s cloud_relay %v", b.Tier, b.Flags[flagspkg.FlagCloudRelay])
	}
}

func TestUserOverride_OnlyThatUserAndAudited(t *testing.T) {
	p := newPGHome(t)
	_ = p.bundle(viewerP()) // warm the cache: the write must drop it
	got := p.want(p.do(http.MethodPost, "/api/admin/flags/overrides", map[string]any{
		"flag_key": flagspkg.FlagEarlyAccess, "scope": "user", "scope_value": viewerID, "value": true,
	}, adminP()), http.StatusCreated)
	id, _ := got["id"].(string)
	if p.bundle(viewerP()).Flags[flagspkg.FlagEarlyAccess] != true {
		t.Error("the user's override applies on the next call")
	}
	other := &principal.Principal{UserID: p.other}
	if p.bundle(other).Flags[flagspkg.FlagEarlyAccess] != false {
		t.Error("other users are unchanged")
	}
	if p.auditCount("flags.override.created") != 1 {
		t.Error("the create is audited under category flags")
	}

	p.want(p.do(http.MethodPatch, "/api/admin/flags/overrides/"+id, map[string]any{"value": false}, adminP()), http.StatusOK)
	if p.bundle(viewerP()).Flags[flagspkg.FlagEarlyAccess] != false {
		t.Error("the patched value applies")
	}
	p.want(p.do(http.MethodPatch, "/api/admin/flags/overrides/"+id, map[string]any{"value": "yes"}, adminP()), http.StatusUnprocessableEntity)
	p.want(p.do(http.MethodDelete, "/api/admin/flags/overrides/"+id, nil, adminP()), http.StatusNoContent)
	p.want(p.do(http.MethodDelete, "/api/admin/flags/overrides/"+id, nil, adminP()), http.StatusNotFound)
	if p.auditCount("flags.override.updated") != 1 || p.auditCount("flags.override.deleted") != 1 {
		t.Error("the update and the delete are audited")
	}
}

func TestOverride_PaidFeatureCannotBeGranted(t *testing.T) {
	p := newPGHome(t)
	p.want(p.do(http.MethodPost, "/api/admin/flags/overrides", map[string]any{
		"flag_key": flagspkg.FlagFederation, "scope": "global", "value": true,
	}, adminP()), http.StatusCreated)
	if p.bundle(viewerP()).Flags[flagspkg.FlagFederation] != false {
		t.Error("federation is not in the home license: no override grants it")
	}
	p.want(p.do(http.MethodPost, "/api/admin/flags/overrides", map[string]any{
		"flag_key": flagspkg.FlagCloudRelay, "scope": "tier", "scope_value": "home", "value": false,
	}, adminP()), http.StatusCreated)
	if p.bundle(viewerP()).Flags[flagspkg.FlagCloudRelay] != false {
		t.Error("a tier override may turn a licensed feature off")
	}
}

func TestOverride_Validation(t *testing.T) {
	p := newPGHome(t)
	for _, body := range []map[string]any{
		{"flag_key": "early_access", "scope": "tier", "scope_value": "gold", "value": true},
		{"flag_key": "early_access", "scope": "global", "scope_value": "x", "value": true},
		{"flag_key": "early_access", "scope": "user", "scope_value": uuid.NewString(), "value": true},
		{"flag_key": "early_access", "scope": "cohort", "scope_value": "Bad Name", "value": true},
		{"flag_key": "early_access", "scope": "global", "value": "on"},
		{"flag_key": "made.up", "scope": "global", "value": map[string]any{"a": 1}},
		{"flag_key": "early_access", "scope": "global", "value": true, "expires_at": time.Now().Add(-time.Hour)},
		{"flag_key": "Bad Key", "scope": "global", "value": true},
	} {
		if rec := p.do(http.MethodPost, "/api/admin/flags/overrides", body, adminP()); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d, want 422", body, rec.Code)
		}
	}
	// an undeclared key is stored (a newer release may declare it) and flagged
	got := p.want(p.do(http.MethodPost, "/api/admin/flags/overrides", map[string]any{
		"flag_key": "made.up", "scope": "global", "value": 3,
	}, adminP()), http.StatusCreated)
	if got["unknown"] != true {
		t.Errorf("an undeclared key is marked unknown: %v", got)
	}
	if _, ok := p.bundle(viewerP()).Flags["made.up"]; ok {
		t.Error("an undeclared key stays out of the bundle")
	}
}

func TestAdminView_StaleAndResolution(t *testing.T) {
	p := newPGHome(t)
	for _, v := range []bool{true, false} {
		p.want(p.do(http.MethodPost, "/api/admin/flags/overrides", map[string]any{
			"flag_key": flagspkg.FlagEarlyAccess, "scope": "user", "scope_value": viewerID, "value": v,
		}, adminP()), http.StatusCreated)
		time.Sleep(5 * time.Millisecond) // distinct created_at
	}
	got := p.want(p.do(http.MethodGet, "/api/admin/flags?user_id="+viewerID, nil, adminP()), http.StatusOK)
	rows := got["overrides"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["stale"] != true || rows[1].(map[string]any)["stale"] != nil {
		t.Errorf("the older of two same-scope rows is stale: %v", rows)
	}
	flags := got["resolved"].(map[string]any)["flags"].(map[string]any)
	if flags[flagspkg.FlagEarlyAccess] != false {
		t.Errorf("the newest row wins: %v", flags)
	}
	p.want(p.do(http.MethodGet, "/api/admin/flags?user_id="+uuid.NewString(), nil, adminP()), http.StatusNotFound)
}

func TestCohort_AdminAddRemoveTakesEffect(t *testing.T) {
	p := newPGHome(t)
	_ = p.bundle(viewerP())
	got := p.want(p.do(http.MethodPost, "/api/admin/cohorts/early_access/users",
		map[string]any{"user_ids": []string{viewerID}}, adminP()), http.StatusOK)
	if got["added"] != float64(1) {
		t.Errorf("added: %v", got)
	}
	if p.bundle(viewerP()).Flags[flagspkg.FlagEarlyAccess] != true {
		t.Error("a member gets the cohort's flag on the next call")
	}
	p.want(p.do(http.MethodDelete, "/api/admin/cohorts/early_access/users/"+viewerID, nil, adminP()), http.StatusNoContent)
	if p.bundle(viewerP()).Flags[flagspkg.FlagEarlyAccess] != false {
		t.Error("moved out of the cohort: the next call reflects it")
	}
	p.want(p.do(http.MethodDelete, "/api/admin/cohorts/early_access/users/"+viewerID, nil, adminP()), http.StatusNotFound)
	if p.auditCount("flags.cohort.added") != 1 || p.auditCount("flags.cohort.removed") != 1 {
		t.Error("cohort writes are audited")
	}
	p.want(p.do(http.MethodPost, "/api/admin/cohorts/early_access/users",
		map[string]any{"user_ids": []string{uuid.NewString()}}, adminP()), http.StatusUnprocessableEntity)
}

func TestCohort_BatchCap(t *testing.T) {
	p := newPGHome(t)
	ids := make([]string, 0, 1001)
	for i := 0; i < 1000; i++ {
		id := uuid.NewString()
		ids = append(ids, id)
		if _, err := p.db.Exec(`INSERT INTO users (id, username, pw_hash) VALUES ($1, $2, 'x')`, id, "bulk"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	got := p.want(p.do(http.MethodPost, "/api/admin/cohorts/big/users", map[string]any{"user_ids": ids}, adminP()), http.StatusOK)
	if got["added"] != float64(1000) {
		t.Errorf("1000 ids are one batch: %v", got)
	}
	p.want(p.do(http.MethodPost, "/api/admin/cohorts/big/users",
		map[string]any{"user_ids": append(ids, uuid.NewString())}, adminP()), http.StatusRequestEntityTooLarge)
}

func TestMeCohorts_OptInOnly(t *testing.T) {
	p := newPGHome(t)
	p.want(p.do(http.MethodPost, "/api/me/cohorts", map[string]any{"cohort": "staff_only"}, viewerP()), http.StatusForbidden)
	p.want(p.do(http.MethodPost, "/api/me/cohorts", map[string]any{"cohort": flagspkg.CohortEarlyAccess}, viewerP()), http.StatusNoContent)
	if p.bundle(viewerP()).Flags[flagspkg.FlagEarlyAccess] != true {
		t.Error("joining early_access turns its flag on")
	}
	items := p.want(p.do(http.MethodGet, "/api/me/cohorts", nil, viewerP()), http.StatusOK)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["joined"] != true {
		t.Errorf("my cohorts: %v", items)
	}
	// a cohort only an admin assigns cannot be left by its member either
	p.want(p.do(http.MethodPost, "/api/admin/cohorts/staff_only/users", map[string]any{"user_ids": []string{viewerID}}, adminP()), http.StatusOK)
	p.want(p.do(http.MethodDelete, "/api/me/cohorts/staff_only", nil, viewerP()), http.StatusForbidden)
	p.want(p.do(http.MethodDelete, "/api/me/cohorts/"+flagspkg.CohortEarlyAccess, nil, viewerP()), http.StatusNoContent)
	if p.bundle(viewerP()).Flags[flagspkg.FlagEarlyAccess] != false {
		t.Error("leaving early_access turns its flag off")
	}
}

func TestAdminRoutes_RefusedOnARealHomeToo(t *testing.T) {
	p := newPGHome(t)
	body := map[string]any{"flag_key": "early_access", "scope": "global", "value": true}
	for _, who := range []*principal.Principal{nil, viewerP(), playP()} {
		if rec := p.do(http.MethodPost, "/api/admin/flags/overrides", body, who); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%+v: %d, want refused", who, rec.Code)
		}
	}
	var n int
	_ = p.db.QueryRow(`SELECT COUNT(*) FROM feature_flag_overrides`).Scan(&n)
	if n != 0 {
		t.Errorf("a refused write wrote %d rows", n)
	}
	p.want(p.do(http.MethodPost, "/api/admin/flags/overrides", body, adminP()), http.StatusCreated)
}
