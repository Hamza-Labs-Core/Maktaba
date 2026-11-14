//go:build integration

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/moderation"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// modHome is a claimed home with one published channel the cloud knows the edge id of.
func modHome(t *testing.T, e *env, owner *client, slug string) (bhome, string, string) {
	t.Helper()
	h := claimHome(e, owner, slug)
	edge := broadcast.ChannelID(h.id, "7")
	var ch string
	if err := e.pool.DB.QueryRow(`INSERT INTO broadcast_channels (server_id, slug, access, agreement_version, edge_channel)
		VALUES ($1, 'news', 'open', 1, $2) RETURNING id::text`, h.id, edge).Scan(&ch); err != nil {
		t.Fatal(err)
	}
	return h, ch, edge
}

func notice(slug string, item int) map[string]any {
	return map[string]any{"claimant_name": "Studio A", "claimant_email": "rights@studio.test", "claimant_address": "1 Road, City",
		"work": fmt.Sprintf("Film %d", item), "urls": []string{"https://watch.maktaba.tv/" + slug + "/news"},
		"at": "2026-11-03T20:00:00Z", "explanation": "the whole film", "good_faith": true, "accuracy": true, "signature": "A. Person"}
}

// instructions verifies a home's signed batch with the broadcast key.
func instructions(t *testing.T, key broadcast.Key, text string) []moderation.Instruction {
	t.Helper()
	rest, ok := strings.CutPrefix(text, broadcast.ControlPrefix)
	if !ok {
		t.Fatalf("not signed: %s", text)
	}
	body, sig, _ := strings.Cut(rest, ".")
	s, _ := base64.RawURLEncoding.DecodeString(sig)
	if !ed25519.Verify(key.Priv.Public().(ed25519.PublicKey), []byte(broadcast.ControlDomain+"."+body), s) {
		t.Fatal("the instructions do not verify")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	var p struct {
		Typ          string                   `json:"typ"`
		Instructions []moderation.Instruction `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Typ != moderation.InstructionsType {
		t.Fatalf("payload %s %v", raw, err)
	}
	return p.Instructions
}

// Story 50.24 on the api role's real routers: anyone files a notice or a report; only a service
// admin acts on one (T08: a customer 401, staff with no role or another role 403); a takedown
// reaches the home signed, slates the channel at the edge until the slate's own end, whatever the home answers (T03, T04), and
// records the strike and the statement; a counter-notice opens the restore window 10 business days
// later (T06); the third strike ends broadcasting (T05); an urgent report pages (T07); a home sees
// only its own records; every staff action is audited, and the records export.
func TestModerationNoticeTakedownCounterRestore(t *testing.T) {
	keyPath := ingestKeyFile(t)
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Broadcast.KeyPath = keyPath
		c.Broadcast.IngestURL = "" // no edge: the push is skipped
	})
	key, err := broadcast.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	customer := e.registerVerified("viewer@example.test")
	noRole := e.staff("helper@example.test", "")
	supportAcct := e.enrolStaff("support@example.test", "")
	e.exec(`UPDATE staff_accounts SET roles = '{support}' WHERE id = $1`, supportAcct.id)
	support := e.staffSignIn(supportAcct)
	admin := e.staff("ops@example.test", stores.StaffRoleServiceAdmin)
	owner := e.registerVerified("owner@example.test")
	home, ch, edge := modHome(t, e, owner, "mod-home")
	otherOwner := e.registerVerified("other@example.test")
	other, _, _ := modHome(t, e, otherOwner, "other-home")

	// anyone, with no account, files a notice; an incomplete one is told what is missing
	if r := e.client().do("POST", "/v1/moderation/notices", map[string]any{"claimant_name": "A"}); r.code != http.StatusUnprocessableEntity ||
		r.errCode() != "notice_incomplete" {
		t.Fatalf("an incomplete notice = %d %s", r.code, r.body)
	}
	nid := e.client().mustStatus(e.client().do("POST", "/v1/moderation/notices", notice("mod-home", 1)), http.StatusAccepted).json(t)["notice_id"].(string)
	if e.count(`SELECT count(*) FROM moderation_notices WHERE id = $1 AND server_id = $2 AND channel_id = $3`, nid, home.id, ch) != 1 {
		t.Fatal("the notice did not resolve to the channel")
	}

	// T08: every admin route answers only a service admin
	takedown := map[string]any{"basis": "notice", "notice_id": nid, "scope": "item", "airing_now": true, "reason": "valid notice"}
	routes := [][2]string{{"GET", "/v1/admin/moderation"}, {"POST", "/v1/admin/moderation/actions"},
		{"POST", "/v1/admin/moderation/notices"}, {"POST", "/v1/admin/moderation/notices/" + nid + "/reject"},
		{"POST", "/v1/admin/moderation/actions/" + nid + "/restore"}, {"POST", "/v1/admin/moderation/strikes/" + nid + "/withdraw"},
		{"POST", "/v1/admin/moderation/publishers/" + nid + "/reinstate"}, {"GET", "/v1/admin/moderation/export"}}
	for _, rt := range routes {
		if r := customer.do(rt[0], rt[1], takedown); r.code != http.StatusUnauthorized {
			t.Errorf("a customer %s %s = %d %s", rt[0], rt[1], r.code, r.body)
		}
		for name, c := range map[string]*client{"no role": noRole, "support": support} {
			if r := c.do(rt[0], rt[1], takedown); r.code != http.StatusForbidden || r.errCode() != "admin_access_denied" {
				t.Errorf("staff with %s %s %s = %d %s", name, rt[0], rt[1], r.code, r.body)
			}
		}
	}
	// a customer is no home either
	if r := customer.do("GET", "/v1/broadcast/moderation", nil); r.code != http.StatusUnauthorized {
		t.Errorf("a customer reads a home's records = %d", r.code)
	}
	if n := e.count(`SELECT count(*) FROM moderation_actions`); n != 0 {
		t.Fatalf("a refused request acted (%d)", n)
	}

	// every action needs its reason
	if r := admin.do("POST", "/v1/admin/moderation/actions", map[string]any{"basis": "notice", "notice_id": nid, "scope": "item"}); r.code != http.StatusBadRequest {
		t.Fatalf("a takedown with no reason = %d %s", r.code, r.body)
	}
	// T03, T04: the service admin accepts; the channel is slated at the edge now
	a := admin.mustStatus(admin.do("POST", "/v1/admin/moderation/actions", takedown), http.StatusCreated).json(t)
	aid := a["id"].(string)
	if a["strike"] != true || a["level"] != "notice" || a["home_state"] != "pending" || a["revocation"] == "" {
		t.Fatalf("takedown = %v", a)
	}
	if e.count(`SELECT count(*) FROM broadcast_revocations WHERE kind = 'programme' AND ref = $1 AND action = 'slate'
		AND lifted_at IS NULL AND until_at > now()`, edge) != 1 {
		t.Fatal("no programme slate at the edge")
	}
	if r := admin.do("POST", "/v1/admin/moderation/actions", takedown); r.code != http.StatusConflict {
		t.Errorf("a second action on the decided notice = %d", r.code)
	}
	if e.count(`SELECT count(*) FROM moderation_statements WHERE server_id = $1 AND kind = 'takedown'`, home.id) != 1 {
		t.Fatal("no statement of reasons")
	}
	if _, ok := e.mail.Last("moderation_takedown", "owner@example.test"); !ok {
		t.Error("the publisher was not mailed")
	}

	// the home pulls its signed instruction; the other home gets none and can't ack it
	text := home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/takedowns", nil), http.StatusOK)
	ins := instructions(t, key, string(text.body))
	if len(ins) != 1 || ins[0].ID != aid || ins[0].Channel != ch || ins[0].At == nil || ins[0].Kind != "takedown" || ins[0].Scope != "item" {
		t.Fatalf("instructions = %+v", ins)
	}
	if got := instructions(t, key, string(other.agent.mustStatus(other.agent.do("GET", "/v1/broadcast/takedowns", nil), http.StatusOK).body)); len(got) != 0 {
		t.Fatalf("another home sees %+v", got)
	}
	until := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	ack := map[string]any{"result": "applied", "item_ref": "vid-1", "programme": "Film 1", "airing_until": until}
	if r := other.agent.do("POST", "/v1/broadcast/takedowns/"+aid+"/ack", ack); r.code != http.StatusNotFound {
		t.Errorf("another home's ack = %d", r.code)
	}
	home.agent.mustStatus(home.agent.do("POST", "/v1/broadcast/takedowns/"+aid+"/ack", ack), http.StatusNoContent)
	// the home is the publisher the notice is about: its word neither shortens nor lifts the slate
	if e.count(`SELECT count(*) FROM broadcast_revocations WHERE kind = 'programme' AND ref = $1 AND lifted_at IS NULL
			AND until_at <> $2`, edge, until) != 1 {
		t.Fatal("the home's ack changed the slate")
	}
	if e.count(`SELECT count(*) FROM moderation_actions WHERE id = $1 AND home_state = 'applied' AND item_ref = 'vid-1'
			AND home_detail LIKE '%on air until%'`, aid) != 1 {
		t.Fatal("the ack was not recorded with the slot end it reported")
	}

	// the home's page: its statement with the counter-notice offered, its standing
	view := home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/moderation", nil), http.StatusOK).json(t)
	sts := view["statements"].([]any)
	if view["strikes"].(float64) != 1 || view["strike_limit"].(float64) != 3 || len(sts) != 1 || sts[0].(map[string]any)["can_counter"] != true {
		t.Fatalf("home view = %v", view)
	}
	if v := other.agent.mustStatus(other.agent.do("GET", "/v1/broadcast/moderation", nil), http.StatusOK).json(t); len(v["statements"].([]any)) != 0 {
		t.Fatalf("another home sees %v", v)
	}

	// T06: the counter-notice; restore waits for the window, then re-enables through the home
	counter := map[string]any{"action_id": aid, "name": "Owner", "address": "2 Street", "email": "owner@example.test",
		"explanation": "my own recording", "mistake": true, "consent": true, "signature": "Owner"}
	if r := home.agent.do("POST", "/v1/broadcast/moderation/counter-notices", map[string]any{"action_id": aid}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("an incomplete counter-notice = %d", r.code)
	}
	if r := other.agent.do("POST", "/v1/broadcast/moderation/counter-notices", counter); r.code != http.StatusNotFound {
		t.Fatalf("another home's counter-notice = %d", r.code)
	}
	home.agent.mustStatus(home.agent.do("POST", "/v1/broadcast/moderation/counter-notices", counter), http.StatusCreated)
	if _, ok := e.mail.Last("moderation_counter_notice_copy", "rights@studio.test"); !ok {
		t.Error("the claimant was not sent a copy")
	}
	if r := admin.do("POST", "/v1/admin/moderation/actions/"+aid+"/restore", map[string]string{"reason": "counter-notice"}); r.code != http.StatusConflict ||
		r.errCode() != "restore_window_closed" {
		t.Fatalf("an early restore = %d %s", r.code, r.body)
	}
	e.exec(`UPDATE moderation_counter_notices SET filed_at = now() - interval '20 days' WHERE action_id = $1`, aid)
	q := admin.mustStatus(admin.do("GET", "/v1/admin/moderation", nil), http.StatusOK).json(t)
	if cs := q["counter_notices"].([]any); len(cs) != 1 || cs[0].(map[string]any)["restore_open"] != true {
		t.Fatalf("the queue does not offer restore: %v", q["counter_notices"])
	}
	restored := admin.mustStatus(admin.do("POST", "/v1/admin/moderation/actions/"+aid+"/restore", map[string]string{"reason": "no lawsuit"}),
		http.StatusCreated).json(t)
	ins = instructions(t, key, string(home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/takedowns", nil), http.StatusOK).body))
	if len(ins) != 1 || ins[0].Kind != "restore" || ins[0].ID != restored["id"] || ins[0].ItemRef != "vid-1" || ins[0].Restores != aid {
		t.Fatalf("the restore instruction = %+v", ins)
	}
	if e.count(`SELECT count(*) FROM broadcast_revocations WHERE kind = 'programme' AND ref = $1 AND lifted_at IS NULL`, edge) != 0 {
		t.Error("the slate outlived the restore")
	}
	if e.count(`SELECT count(*) FROM moderation_strikes WHERE action_id = $1 AND withdrawn_at IS NOT NULL`, aid) != 1 {
		t.Error("the successful counter-notice did not withdraw the strike")
	}

	// T05: three valid notices in 12 months end broadcasting for the publisher
	for i := 2; i <= 4; i++ {
		id := e.client().mustStatus(e.client().do("POST", "/v1/moderation/notices", notice("mod-home", i)), http.StatusAccepted).json(t)["notice_id"].(string)
		r := admin.mustStatus(admin.do("POST", "/v1/admin/moderation/actions", map[string]any{"basis": "notice", "notice_id": id,
			"scope": "item", "item_ref": fmt.Sprintf("vid-%d", i), "reason": "valid notice"}), http.StatusCreated).json(t)
		want := map[int]string{2: "notice", 3: "warning", 4: "terminate"}[i]
		if r["level"] != want {
			t.Fatalf("strike %d: level %v, want %s", i, r["level"], want)
		}
	}
	uid := e.userID("owner@example.test")
	if e.count(`SELECT count(*) FROM broadcast_revocations WHERE kind = 'publisher' AND ref = $1 AND action = 'terminate'
		AND reason = 'repeat_infringer' AND lifted_at IS NULL`, uid) != 1 || e.count(`SELECT count(*) FROM moderation_terminations
		WHERE publisher_id = $1 AND reinstated_at IS NULL`, uid) != 1 {
		t.Fatal("the third strike did not end broadcasting")
	}
	if e.count(`SELECT count(*) FROM moderation_statements WHERE server_id = $1 AND kind IN ('strike_warning', 'terminated')`, home.id) != 2 {
		t.Error("the warning and the end were not told")
	}
	if v := home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/moderation", nil), http.StatusOK).json(t); v["terminated"] != true {
		t.Errorf("the home is not told it is terminated: %v", v)
	}
	admin.mustStatus(admin.do("POST", "/v1/admin/moderation/publishers/"+uid+"/reinstate", map[string]string{"reason": "appeal upheld"}), http.StatusNoContent)
	if e.count(`SELECT count(*) FROM broadcast_revocations WHERE kind = 'publisher' AND ref = $1 AND lifted_at IS NULL`, uid) != 0 {
		t.Error("the reinstatement left the publisher off air")
	}

	// T07: reports land in the queue; the urgent one pages; the home sees a content report without
	// the reporter's email
	for _, c := range []string{"copyright", "abuse", "other", "illegal"} {
		e.client().mustStatus(e.client().do("POST", "/v1/moderation/reports", map[string]any{"category": c,
			"channel": "https://watch.maktaba.tv/mod-home/news", "email": "viewer@example.test", "text": "seen at 20:00"}), http.StatusAccepted)
	}
	if !strings.Contains(e.logs.String(), "code="+moderation.AlertUrgent) {
		t.Error("the urgent report paged no one")
	}
	v := home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/moderation", nil), http.StatusOK)
	if reps := v.json(t)["reports"].([]any); len(reps) != 4 || strings.Contains(string(v.body), "viewer@example.test") {
		t.Errorf("the home's reports = %s", v.body)
	}

	// every staff action is audited with its reason; the records export for a lawyer
	if e.count(`SELECT count(*) FROM moderation_audit WHERE actor_email = 'ops@example.test' AND verb IN ('takedown', 'restore', 'reinstate')
		AND reason <> ''`) != 6 {
		t.Error("the staff actions are not all audited")
	}
	csv := admin.mustStatus(admin.do("GET", "/v1/admin/moderation/export?format=csv", nil), http.StatusOK)
	if !strings.HasPrefix(string(csv.body), "table,id,at,row") || !strings.Contains(string(csv.body), "moderation_strikes") {
		t.Errorf("the CSV export = %.200s", csv.body)
	}
	js := admin.mustStatus(admin.do("GET", "/v1/admin/moderation/export", nil), http.StatusOK).json(t)
	if len(js["records"].([]any)) < 20 {
		t.Errorf("the JSON export has %d records", len(js["records"].([]any)))
	}
}
