//go:build integration

package main

import (
	"crypto/ed25519"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Story 50.25 on the api role's real routers: the kill switch's admin routes answer only a
// service admin (T08), every add and lift is audited and moves the list's version, and the pull
// gives a home the signed list with the entry in it.
func TestBroadcastKillSwitchRolesAuditAndPull(t *testing.T) {
	key := ingestKeyFile(t)
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Broadcast.KeyPath = key
		c.Broadcast.IngestURL = "" // no edge in the test: the push is skipped, the pull serves
	})
	customer := e.registerVerified("viewer@example.test")
	noRole := e.staff("helper@example.test", "")
	// any other staff role (the CLI grants service_admin only today; a later role is set here)
	supportAcct := e.enrolStaff("support@example.test", "")
	e.exec(`UPDATE staff_accounts SET roles = '{support}' WHERE id = $1`, supportAcct.id)
	support := e.staffSignIn(supportAcct)
	admin := e.staff("ops@example.test", stores.StaffRoleServiceAdmin)
	owner := e.registerVerified("owner@example.test")
	home := claimHome(e, owner, "kill-home")

	ch := broadcast.ChannelID(home.id, "7")
	add := map[string]any{"kind": "channel", "ref": ch, "action": "off_air", "reason": "notice", "note": "a valid notice"}
	const list = "/v1/admin/broadcast/revocations"

	// T08: a customer's token is no staff token (401 no_session); a staff account with no role
	// and one with another role are 403 admin_access_denied, on every route
	for _, rt := range [][2]string{{"GET", list}, {"POST", list}, {"POST", list + "/r-1/lift"}} {
		if r := customer.do(rt[0], rt[1], add); r.code != http.StatusUnauthorized {
			t.Errorf("a customer %s %s = %d %s", rt[0], rt[1], r.code, r.body)
		}
		for name, c := range map[string]*client{"no role": noRole, "support": support} {
			if r := c.do(rt[0], rt[1], add); r.code != http.StatusForbidden || r.errCode() != "admin_access_denied" {
				t.Errorf("staff with %s %s %s = %d %s, want 403 admin_access_denied", name, rt[0], rt[1], r.code, r.body)
			}
		}
	}
	if n := e.count(`SELECT count(*) FROM broadcast_revocations`); n != 0 {
		t.Fatalf("a refused request added %d entries", n)
	}
	// the user listener has no such route at all
	if r := customer.do("GET", "/v1/broadcast/revocations", nil); r.code != http.StatusUnauthorized {
		t.Errorf("the pull without a home's credential = %d %s", r.code, r.body)
	}

	// a bad entry is refused before anything is written
	if r := admin.do("POST", list, map[string]any{"kind": "channel", "ref": ch, "action": "terminate", "reason": "notice"}); r.code != http.StatusBadRequest {
		t.Fatalf("a channel terminate = %d %s", r.code, r.body)
	}

	// the service admin adds: 201, version 1, an audit row naming the staff account
	r := admin.mustStatus(admin.do("POST", list, add), http.StatusCreated).json(t)
	entry := r["entry"].(map[string]any)
	id := entry["id"].(string)
	if r["version"].(float64) != 1 || entry["added_by"] != "ops@example.test" {
		t.Fatalf("add = %v", r)
	}
	if n := e.count(`SELECT count(*) FROM broadcast_revocation_audit a JOIN staff_accounts s ON s.id::text = a.actor_id
		WHERE a.action = 'add' AND a.version = 1 AND s.email = 'ops@example.test' AND a.note = 'a valid notice'`); n != 1 {
		t.Fatalf("the add is not audited (%d rows)", n)
	}

	// the home pulls the signed list and finds the entry
	text := home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/revocations", nil), http.StatusOK)
	k, err := broadcast.LoadKey(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := broadcast.VerifyRevocations(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, string(text.body))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Entries) != 1 || got.Entries[0].Ref != ch || got.Entries[0].ID != id {
		t.Fatalf("the pulled list = %+v", got)
	}
	if !got.ExpiresAt.After(time.Now().Add(47 * time.Hour)) {
		t.Errorf("the list expires at %s", got.ExpiresAt)
	}

	// the console's view: the entry, who added it, the audit
	view := admin.mustStatus(admin.do("GET", list, nil), http.StatusOK).json(t)
	if view["version"].(float64) != 1 || len(view["entries"].([]any)) != 1 || len(view["audit"].([]any)) != 1 || view["signing"] != true {
		t.Fatalf("view = %v", view)
	}

	// lift: version 2, audited, and the next pull has no entry; a second lift is 404
	admin.mustStatus(admin.do("POST", list+"/"+id+"/lift", map[string]string{"note": "notice withdrawn"}), http.StatusOK)
	if r := admin.do("POST", list+"/"+id+"/lift", nil); r.code != http.StatusNotFound {
		t.Errorf("a second lift = %d %s", r.code, r.body)
	}
	if n := e.count(`SELECT count(*) FROM broadcast_revocation_audit WHERE action = 'lift' AND version = 2 AND note = 'notice withdrawn'`); n != 1 {
		t.Fatalf("the lift is not audited (%d rows)", n)
	}
	text = home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/revocations", nil), http.StatusOK)
	got, _ = broadcast.VerifyRevocations(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, string(text.body))
	if got.Version != 2 || len(got.Entries) != 0 {
		t.Fatalf("after the lift = %+v", got)
	}
	// the gate audits every request it served too
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.request' AND detail->>'route' LIKE '/v1/admin/broadcast/revocations%'`); n < 4 {
		t.Errorf("admin.request rows for the kill switch: %d", n)
	}
}
