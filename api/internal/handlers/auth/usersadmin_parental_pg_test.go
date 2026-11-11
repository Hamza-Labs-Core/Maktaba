package auth

// Epic 57 story 57.02: POST /api/users makes a restricted account in one request,
// and no request makes a restricted account an admin. Against a real migrated
// Postgres (slot 0221); runs when MAKTABA_TEST_PG_URL is set.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

const parentalTestPassword = "correct horse battery staple"

type parentalUsers struct {
	t     *testing.T
	db    *sql.DB
	h     *Handler
	admin string
}

func newParentalUsers(t *testing.T) *parentalUsers {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	store := users.New(db)
	admin, err := store.Create(context.Background(), users.CreateInput{Username: "pu-admin", Password: parentalTestPassword, IsAdmin: true})
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return &parentalUsers{t: t, db: db, h: &Handler{UserAdmin: store, DB: db}, admin: admin.ID}
}

func (p *parentalUsers) call(as *principal.Principal, method, id string, body any) (int, map[string]any) {
	p.t.Helper()
	raw, _ := json.Marshal(body)
	target := "/api/users"
	if id != "" {
		target += "/" + id
	}
	r := httptest.NewRequest(method, target, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	if as != nil {
		r = r.WithContext(principal.WithPrincipal(r.Context(), as))
	}
	rec := httptest.NewRecorder()
	if id == "" {
		p.h.AdminCreateUser(rec, r)
	} else {
		p.h.AdminUpdateUser(rec, withChiParam(r, "id", id))
	}
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func (p *parentalUsers) asAdmin() *principal.Principal {
	return &principal.Principal{UserID: p.admin, IsAdmin: true, Source: principal.SourceJWT}
}

func (p *parentalUsers) count(q string, args ...any) int {
	p.t.Helper()
	var n int
	if err := p.db.QueryRow(q, args...).Scan(&n); err != nil {
		p.t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestCreateRestrictedAccount(t *testing.T) {
	p := newParentalUsers(t)
	_, g := p.call(p.asAdmin(), http.MethodPost, "", map[string]any{"username": "pu-guardian", "password": parentalTestPassword})
	guardian, _ := g["id"].(string)
	if guardian == "" {
		t.Fatalf("seed guardian: %v", g)
	}
	code, out := p.call(p.asAdmin(), http.MethodPost, "", map[string]any{
		"username": "pu-child", "password": parentalTestPassword,
		"parental": map[string]any{"max_tier": 9, "allow_only": true, "guardians": []string{guardian}},
	})
	if code != http.StatusCreated {
		t.Fatalf("create restricted = %d %v", code, out)
	}
	child := out["id"].(string)
	var tier, version int
	var unrated string
	var allowOnly bool
	if err := p.db.QueryRow(`SELECT max_tier, unrated, allow_only, version FROM parental_policies WHERE user_id = $1`, child).
		Scan(&tier, &unrated, &allowOnly, &version); err != nil {
		t.Fatalf("the new account has no policy: %v", err)
	}
	if tier != 9 || unrated != "hide" || !allowOnly || version != 2 {
		// version 2: made (1), then the guardian named
		t.Fatalf("policy = tier %d unrated %s allow_only %v version %d", tier, unrated, allowOnly, version)
	}
	if n := p.count(`SELECT count(*) FROM parental_guardians WHERE child_id = $1 AND guardian_id = $2`, child, guardian); n != 1 {
		t.Fatal("the guardian was not named")
	}
	if n := p.count(`SELECT count(*) FROM audit_log WHERE action LIKE 'parental.%' AND target_id = $1 AND actor_user_id = $2`, child, p.admin); n != 2 {
		t.Fatalf("%d parental audit rows for the new account, want 2", n)
	}

	// no request makes it an admin
	code, out = p.call(p.asAdmin(), http.MethodPatch, child, map[string]any{"is_admin": true})
	if code != http.StatusConflict || out["code"] != "restricted_admin" || out["type"] != TypeRestrictedAdmin {
		t.Fatalf("admin on a restricted account = %d %v", code, out)
	}
	if n := p.count(`SELECT count(*) FROM users WHERE id = $1 AND is_admin`, child); n != 0 {
		t.Fatal("the restricted account became an admin")
	}
	// its name still changes
	if code, out := p.call(p.asAdmin(), http.MethodPatch, child, map[string]any{"username": "pu-child-renamed"}); code != http.StatusOK {
		t.Fatalf("rename a restricted account = %d %v", code, out)
	}
}

// A refused request leaves no account behind.
func TestCreateRestrictedAccountRefusals(t *testing.T) {
	p := newParentalUsers(t)
	usersBefore := p.count(`SELECT count(*) FROM users`)
	cases := []struct {
		name string
		body map[string]any
		want int
		code string
	}{
		{"an admin who is restricted", map[string]any{"is_admin": true, "parental": map[string]any{"max_tier": 9}}, 409, "restricted_admin"},
		{"no tier", map[string]any{"parental": map[string]any{}}, 422, ""},
		{"tier 12", map[string]any{"parental": map[string]any{"max_tier": 12}}, 422, ""},
		{"bad unrated", map[string]any{"parental": map[string]any{"max_tier": 9, "unrated": "sometimes"}}, 422, ""},
		{"a guardian who is not there", map[string]any{"parental": map[string]any{"max_tier": 9,
			"guardians": []string{"00000000-0000-4000-8000-00000057eeee"}}}, 422, ""},
		{"a guardian id that is none", map[string]any{"parental": map[string]any{"max_tier": 9, "guardians": []string{"mum"}}}, 422, ""},
	}
	for i, c := range cases {
		c.body["username"] = "pu-refused-" + string(rune('a'+i))
		c.body["password"] = parentalTestPassword
		code, out := p.call(p.asAdmin(), http.MethodPost, "", c.body)
		if code != c.want || (c.code != "" && out["code"] != c.code) {
			t.Errorf("%s = %d %v, want %d %s", c.name, code, out, c.want, c.code)
		}
	}
	if n := p.count(`SELECT count(*) FROM users`); n != usersBefore {
		t.Fatalf("refused requests left %d accounts behind", n-usersBefore)
	}
	if n := p.count(`SELECT count(*) FROM parental_policies`); n != 0 {
		t.Fatalf("refused requests left %d policies behind", n)
	}

	// a restricted account cannot be named as a guardian of a new one
	_, first := p.call(p.asAdmin(), http.MethodPost, "", map[string]any{"username": "pu-kid", "password": parentalTestPassword,
		"parental": map[string]any{"max_tier": 13}})
	kid, _ := first["id"].(string)
	if kid == "" {
		t.Fatalf("seed child: %v", first)
	}
	code, out := p.call(p.asAdmin(), http.MethodPost, "", map[string]any{"username": "pu-kid-two", "password": parentalTestPassword,
		"parental": map[string]any{"max_tier": 13, "guardians": []string{kid}}})
	if code != http.StatusConflict || out["code"] != "restricted_guardian" {
		t.Fatalf("a restricted guardian = %d %v", code, out)
	}
	if n := p.count(`SELECT count(*) FROM users WHERE username = 'pu-kid-two'`); n != 0 {
		t.Fatal("the refused account was left behind")
	}
}

// Making a restricted account is the admin's alone: no one, a viewer, a restricted
// child, a guardian.
func TestCreateRestrictedAccountRoles(t *testing.T) {
	p := newParentalUsers(t)
	mk := func(name string, parental map[string]any) string {
		t.Helper()
		body := map[string]any{"username": name, "password": parentalTestPassword}
		if parental != nil {
			body["parental"] = parental
		}
		code, out := p.call(p.asAdmin(), http.MethodPost, "", body)
		if code != http.StatusCreated {
			t.Fatalf("seed %s: %d %v", name, code, out)
		}
		return out["id"].(string)
	}
	viewer, guardian, other := mk("pr-viewer", nil), mk("pr-guardian", nil), mk("pr-other-guardian", nil)
	child := mk("pr-child", map[string]any{"max_tier": 9, "guardians": []string{guardian}})
	mk("pr-child-two", map[string]any{"max_tier": 9, "guardians": []string{other}})
	before := p.count(`SELECT count(*) FROM users`)
	body := map[string]any{"username": "pr-new", "password": parentalTestPassword, "parental": map[string]any{"max_tier": 9}}
	for name, c := range map[string]struct {
		as   *principal.Principal
		want int
	}{
		"no one":         {nil, http.StatusUnauthorized},
		"viewer":         {&principal.Principal{UserID: viewer, Source: principal.SourceJWT}, http.StatusForbidden},
		"child":          {&principal.Principal{UserID: child, Source: principal.SourceJWT}, http.StatusForbidden},
		"guardian":       {&principal.Principal{UserID: guardian, Source: principal.SourceJWT}, http.StatusForbidden},
		"other guardian": {&principal.Principal{UserID: other, Source: principal.SourceJWT}, http.StatusForbidden},
	} {
		if code, out := p.call(c.as, http.MethodPost, "", body); code != c.want {
			t.Errorf("create restricted as %s = %d %v, want %d", name, code, out, c.want)
		}
		if code, out := p.call(c.as, http.MethodPatch, child, map[string]any{"is_admin": true}); code != c.want {
			t.Errorf("PATCH as %s = %d %v, want %d", name, code, out, c.want)
		}
	}
	if n := p.count(`SELECT count(*) FROM users`); n != before {
		t.Fatalf("a refused caller made %d accounts", n-before)
	}
	if code, out := p.call(p.asAdmin(), http.MethodPost, "", body); code != http.StatusCreated {
		t.Fatalf("create restricted as the admin = %d %v", code, out)
	}
}
