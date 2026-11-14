//go:build integration

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	edgeh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/edge"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
)

// Story 50.20 on the api role's real router: the Broadcaster tier and Pro's guest sharing.

// withBroadcastPrices sets the broadcast catalog's TBD prices (and Pro's) for one test.
func withBroadcastPrices(t *testing.T) {
	t.Helper()
	withPrices(t)
	old := billingpkg.Broadcaster
	p5000, p600, p1000, p1500 := 5000, 600, 1000, 1500
	billingpkg.Broadcaster.PriceCents = &p5000
	billingpkg.Broadcaster.PAYGPricePer100HCents = &p600
	billingpkg.Broadcaster.ExtraChannelPriceCents = &p1000
	billingpkg.Broadcaster.ExtraViewers100Cents = &p1500
	t.Cleanup(func() { billingpkg.Broadcaster = old })
}

type bhome struct {
	id, secret string
	agent      *client // the home: its id and secret as basic auth
}

// claimHome links a home to owner through a claim code.
func claimHome(e *env, owner *client, slug string) bhome {
	e.t.Helper()
	code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(e.t)["code"].(string)
	agent := e.client()
	red := agent.mustStatus(agent.do("POST", "/v1/servers/claims/redeem",
		map[string]string{"code": code, "name": "Home", "slug": slug}), http.StatusCreated).json(e.t)
	h := bhome{id: red["server_id"].(string), secret: red["server_secret"].(string), agent: agent}
	agent.header = map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(h.id+":"+h.secret))}
	return h
}

// setPlan puts the account and its homes on plan, as the webhook or an admin would.
func setPlan(e *env, uid, plan string) {
	e.exec(`UPDATE users SET plan = $2 WHERE id = $1`, uid, plan)
	e.exec(`UPDATE servers SET plan = $2 WHERE owner_user_id = $1`, uid, plan)
}

// licenceFeatures are the features of the home's licence (nil: none issued).
func licenceFeatures(t *testing.T, h bhome) []string {
	t.Helper()
	r := h.agent.do("GET", "/v1/servers/"+h.id+"/license", nil)
	if r.code != http.StatusOK {
		return nil
	}
	var body struct {
		License license.License `json:"license"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.License.License.Features == nil {
		return []string{}
	}
	return body.License.License.Features
}

func channel(slug, access string, onDemand bool, rungs int) map[string]any {
	return map[string]any{"slug": slug, "access": access, "on_demand": onDemand, "rungs": rungs, "top_kbps": 3000}
}

// The publish checks in order (plan, gate, limits, agreement), the agreement's versions (T11),
// the licence flag, and the service admin's test-publisher grant with its role checks.
func TestBroadcastPublishAndAgreement(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("pub@example.test")
	uid := e.userID("pub@example.test")
	h := claimHome(e, owner, "pubhome")

	// the edge's routes are off without its key
	if r := e.client().do("POST", "/_edge/usage", []byte(`{}`)); r.code != http.StatusServiceUnavailable {
		t.Fatalf("edge without a key: %d %s", r.code, r.body)
	}
	if r := e.client().do("POST", "/v1/broadcast/channels", channel("news", "invite", true, 1)); r.code != http.StatusUnauthorized {
		t.Fatalf("publish without a home's secret: %d", r.code)
	}

	// Free and Basic: publishing needs Pro
	for _, plan := range []string{billingpkg.PlanFree, billingpkg.PlanBasic} {
		setPlan(e, uid, plan)
		if r := h.agent.do("POST", "/v1/broadcast/channels", channel("news", "invite", true, 1)); r.code != http.StatusPaymentRequired || r.errCode() != "broadcast_needs_plan" {
			t.Fatalf("%s publishes: %d %s", plan, r.code, r.body)
		}
	}
	if f := licenceFeatures(t, h); slices.Contains(f, license.FeatureBroadcast) {
		t.Fatalf("basic's licence carries broadcast: %v", f)
	}
	// Pro with the gate shut: not open yet, and the licence doesn't carry the flag
	setPlan(e, uid, billingpkg.PlanPro)
	if r := h.agent.do("POST", "/v1/broadcast/channels", channel("news", "invite", true, 1)); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_enabled" {
		t.Fatalf("pro with the gate shut: %d %s", r.code, r.body)
	}
	if f := licenceFeatures(t, h); f == nil || slices.Contains(f, license.FeatureBroadcast) {
		t.Fatalf("pro's licence with the gate shut: %v", f)
	}

	// the test-publisher grant: a service admin's, with a reason, audited
	path := "/v1/admin/servers/" + h.id + "/broadcast"
	grant := map[string]any{"plan": "", "test_publisher": true, "reason": "staging publisher"}
	if r := owner.do("POST", path, grant); r.code != http.StatusUnauthorized {
		t.Fatalf("a customer granted themself: %d", r.code)
	}
	if r := e.staff("norole@example.test", "").do("POST", path, grant); r.code != http.StatusForbidden {
		t.Fatalf("a staff account without the role granted: %d %s", r.code, r.body)
	}
	ops := e.serviceAdmin("ops@example.test")
	if r := ops.do("POST", path, map[string]any{"test_publisher": true}); r.code != http.StatusBadRequest || r.errCode() != "reason_required" {
		t.Fatalf("no reason: %d %s", r.code, r.body)
	}
	if r := ops.do("POST", path, map[string]any{"plan": "gold", "reason": "x"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("bad plan: %d", r.code)
	}
	ops.mustStatus(ops.do("POST", path, grant), http.StatusNoContent)
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.server.broadcast' AND target_id = $1 AND detail->>'reason' = 'staging publisher'`, h.id) != 1 {
		t.Fatal("the grant wasn't audited")
	}
	if f := licenceFeatures(t, h); !slices.Contains(f, license.FeatureBroadcast) {
		t.Fatalf("a granted pro home's licence: %v", f)
	}

	// guest sharing's limits, each named
	for _, c := range []struct {
		body  map[string]any
		limit string
	}{
		{channel("news", "open", true, 1), "access"},
		{channel("news", "invite", false, 1), "on_demand"},
		{channel("news", "invite", true, 3), "max_rungs"},
	} {
		r := h.agent.do("POST", "/v1/broadcast/channels", c.body)
		if r.code != http.StatusPaymentRequired || r.errCode() != "broadcast_limit" || r.json(t)["limit"] != c.limit {
			t.Fatalf("%s: %d %s", c.limit, r.code, r.body)
		}
	}
	// T11: no agreement, 409; accepted, 201
	if r := h.agent.do("POST", "/v1/broadcast/channels", channel("news", "invite", true, 2)); r.code != http.StatusConflict || r.errCode() != "broadcast_agreement_required" {
		t.Fatalf("without the agreement: %d %s", r.code, r.body)
	}
	if e.count(`SELECT count(*) FROM broadcast_channels`) != 0 {
		t.Fatal("a channel was published without the agreement")
	}
	if a := owner.mustStatus(owner.do("GET", "/v1/broadcast/agreement", nil), http.StatusOK).json(t); a["accepted"] != false || a["version"].(float64) != 1 {
		t.Fatalf("agreement before: %v", a)
	}
	if r := owner.do("POST", "/v1/broadcast/agreement", map[string]int{"version": 99}); r.code != http.StatusConflict {
		t.Fatalf("accepting a version that isn't out: %d", r.code)
	}
	owner.mustStatus(owner.do("POST", "/v1/broadcast/agreement", map[string]int{"version": 1}), http.StatusNoContent)
	if e.count(`SELECT count(*) FROM broadcast_agreements WHERE account_id = $1 AND version = 1 AND ip_hash <> '' AND ip_hash NOT LIKE '198.51.100.%'`, uid) != 1 {
		t.Fatal("the acceptance wasn't stored with the address's hash")
	}
	first := h.agent.mustStatus(h.agent.do("POST", "/v1/broadcast/channels", channel("news", "invite", true, 2)), http.StatusCreated).json(t)
	// a second channel: guest sharing has one
	if r := h.agent.do("POST", "/v1/broadcast/channels", channel("sport", "invite", true, 1)); r.code != http.StatusPaymentRequired ||
		r.json(t)["limit"] != "max_channels" {
		t.Fatalf("a second channel: %d %s", r.code, r.body)
	}
	// a new agreement version: asked again at the next publish
	billingpkg.BroadcastAgreementVersion = 2
	t.Cleanup(func() { billingpkg.BroadcastAgreementVersion = 1 })
	h.agent.mustStatus(h.agent.do("DELETE", "/v1/broadcast/channels/"+first["id"].(string), nil), http.StatusNoContent)
	if r := h.agent.do("POST", "/v1/broadcast/channels", channel("sport", "invite", true, 1)); r.code != http.StatusConflict || r.errCode() != "broadcast_agreement_required" {
		t.Fatalf("a new version not accepted: %d %s", r.code, r.body)
	}
	owner.mustStatus(owner.do("POST", "/v1/broadcast/agreement", map[string]int{"version": 2}), http.StatusNoContent)
	h.agent.mustStatus(h.agent.do("POST", "/v1/broadcast/channels", channel("sport", "invite", true, 1)), http.StatusCreated)

	st := h.agent.mustStatus(h.agent.do("GET", "/v1/broadcast/state", nil), http.StatusOK).json(t)
	if st["plan"] != "guest" || st["max_channels"].(float64) != 1 || st["max_concurrent_viewers"].(float64) != 5 || st["mode"] != "full" {
		t.Fatalf("the home's state: %v", st)
	}
	// story 50.22: the home's publish check 2 reads the owner's acceptance here
	if st["agreement_accepted"] != true {
		t.Fatalf("the owner accepted the agreement: %v", st)
	}
	// the catalog shows both tiers, prices TBD
	cat := e.client().mustStatus(e.client().do("GET", "/v1/billing/plans", nil), http.StatusOK).json(t)
	b := cat["broadcast"].(map[string]any)["broadcaster"].(map[string]any)
	if b["max_channels"].(float64) != 3 || b["viewer_hours_per_month"].(float64) != 5000 || b["price_cents"] != nil {
		t.Fatalf("the catalog's Broadcaster: %v", b)
	}
}

// T08 and T06: the broadcast billing endpoints answer the home's owner only; the add-on needs
// the agreement; the webhook's items set the home's limits; dropping Pro drops Broadcaster and
// its channels past one.
func TestBroadcastBillingRolesAndItems(t *testing.T) {
	withBroadcastPrices(t)
	e := newEnvFull(t, &fakeProvider{}, nil, func(c *config.Config) {
		c.Broadcast.Enabled = true
		c.Broadcast.KeyPath = ingestKeyFile(t) // broadcasting on signs ingest tokens (50.21)
	})
	owner := e.registerVerified("bowner@example.test")
	uid := e.userID("bowner@example.test")
	h := claimHome(e, owner, "bhome")
	setPlan(e, uid, billingpkg.PlanPro)
	e.exec(`INSERT INTO subscriptions (user_id, provider, plan, status, provider_subscription_id, provider_customer_id)
            VALUES ($1, 'testpay', 'pro', 'active', 'sub_b', 'cus_b')`, uid)

	// a user of the home (linked) and a stranger are not its owner
	linked := e.registerVerified("viewer@example.test")
	linkedID := e.userID("viewer@example.test")
	h.agent.mustStatus(h.agent.do("PUT", "/v1/servers/"+h.id+"/links", map[string]any{"cloud_user_ids": []string{linkedID}}), http.StatusNoContent)
	stranger := e.registerVerified("stranger@example.test")
	calls := []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/billing/broadcast?server_id=" + h.id, nil},
		{"PUT", "/v1/billing/broadcast", map[string]any{"server_id": h.id, "enabled": true}},
		{"PATCH", "/v1/billing/broadcast/payg", map[string]any{"server_id": h.id, "enabled": true, "cap_cents": 2000}},
		{"PATCH", "/v1/billing/broadcast/extras", map[string]any{"server_id": h.id, "extra_channels": 1, "extra_viewers_100": 0}},
	}
	for _, c := range calls {
		for name, who := range map[string]*client{"a linked user": linked, "a stranger": stranger} {
			if r := who.do(c.method, c.path, c.body); r.code != http.StatusForbidden || r.errCode() != "not_owner" {
				t.Fatalf("%s: %s %s: %d %s", name, c.method, c.path, r.code, r.body)
			}
		}
		if r := e.client().do(c.method, c.path, c.body); r.code != http.StatusUnauthorized {
			t.Fatalf("no one: %s %s: %d", c.method, c.path, r.code)
		}
	}
	if r := owner.do("GET", "/v1/billing/broadcast?server_id=00000000-0000-0000-0000-000000000000", nil); r.code != http.StatusForbidden {
		t.Fatalf("a home that doesn't exist: %d", r.code)
	}

	// buying Broadcaster asks for the agreement first
	if r := owner.do("PUT", "/v1/billing/broadcast", map[string]any{"server_id": h.id, "enabled": true}); r.code != http.StatusConflict || r.errCode() != "broadcast_agreement_required" {
		t.Fatalf("buying without the agreement: %d %s", r.code, r.body)
	}
	owner.mustStatus(owner.do("POST", "/v1/broadcast/agreement", map[string]int{"version": 1}), http.StatusNoContent)
	owner.mustStatus(owner.do("PUT", "/v1/billing/broadcast", map[string]any{"server_id": h.id, "enabled": true}), http.StatusAccepted)
	if it := e.pay.billing.BroadcastItemsOf("sub_b", h.id); !it.Broadcaster {
		t.Fatalf("the provider's items: %+v", it)
	}
	// until the provider confirms, the home is guest sharing: no pay-as-you-go
	if r := owner.do("PATCH", "/v1/billing/broadcast/payg", calls[2].body); r.code != http.StatusConflict || r.errCode() != "needs_broadcaster" {
		t.Fatalf("pay-as-you-go before Broadcaster: %d %s", r.code, r.body)
	}
	// the provider confirms with 2 extra channels and 100 extra viewers (T06)
	hook := e.client()
	at := time.Now()
	ev := subEvent("evt_b1", "subscription_changed", "sub_b", "cus_b", uid, "active", "pro", at, at.Add(30*24*time.Hour))
	ev.Broadcast = []billingpkg.BroadcastItems{{ServerID: h.id, Broadcaster: true, ExtraChannels: 2, ExtraViewers100: 1}}
	body, sig := signedEvent(ev)
	hook.mustStatus(hook.webhook("testpay", body, sig), http.StatusOK)
	v := owner.mustStatus(owner.do("GET", "/v1/billing/broadcast?server_id="+h.id, nil), http.StatusOK).json(t)
	st := v["state"].(map[string]any)
	if st["plan"] != "broadcaster" || st["max_channels"].(float64) != 5 || st["max_concurrent_viewers"].(float64) != 200 ||
		v["purchasable"] != true || v["enabled"] != true {
		t.Fatalf("after the webhook: %v", v)
	}
	if r := owner.do("PATCH", "/v1/billing/broadcast/payg", map[string]any{"server_id": h.id, "enabled": true, "cap_cents": 0}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("pay-as-you-go without a cap: %d", r.code)
	}
	owner.mustStatus(owner.do("PATCH", "/v1/billing/broadcast/payg", calls[2].body), http.StatusOK)
	if it := e.pay.billing.BroadcastItemsOf("sub_b", h.id); !it.PAYG || it.ExtraChannels != 2 {
		t.Fatalf("the provider's items after pay-as-you-go: %+v", it)
	}
	owner.mustStatus(owner.do("PATCH", "/v1/billing/broadcast/extras", map[string]any{"server_id": h.id, "extra_channels": 3, "extra_viewers_100": 2}), http.StatusAccepted)
	if it := e.pay.billing.BroadcastItemsOf("sub_b", h.id); it.ExtraChannels != 3 || it.ExtraViewers100 != 2 || !it.PAYG {
		t.Fatalf("the provider's items after extras: %+v", it)
	}

	// three channels, one open; then Pro lapses: Broadcaster goes, one channel stays, invite-only
	for _, c := range []map[string]any{channel("one", "open", false, 3), channel("two", "invite", true, 1), channel("three", "invite", false, 1)} {
		h.agent.mustStatus(h.agent.do("POST", "/v1/broadcast/channels", c), http.StatusCreated)
	}
	ev = subEvent("evt_b2", "subscription_cancelled", "sub_b", "cus_b", uid, "canceled", "pro", at.Add(time.Minute), at)
	body, sig = signedEvent(ev)
	hook.mustStatus(hook.webhook("testpay", body, sig), http.StatusOK)
	if e.count(`SELECT count(*) FROM servers WHERE id = $1 AND broadcast_plan IS NULL AND broadcast_extra_channels = 0`, h.id) != 1 {
		t.Fatal("dropping Pro kept Broadcaster")
	}
	if e.count(`SELECT count(*) FROM broadcast_channels WHERE server_id = $1 AND unpublished_at IS NULL AND slug = 'one' AND access = 'invite' AND on_demand`, h.id) != 1 ||
		e.count(`SELECT count(*) FROM broadcast_channels WHERE server_id = $1 AND unpublished_at IS NULL`, h.id) != 1 {
		t.Fatal("the oldest channel didn't stay alone and invite-only")
	}
	if r := h.agent.do("POST", "/v1/broadcast/channels", channel("four", "invite", true, 1)); r.code != http.StatusPaymentRequired || r.errCode() != "broadcast_needs_plan" {
		t.Fatalf("publishing after Pro lapsed: %d %s", r.code, r.body)
	}
}

// edgeClient signs its requests as the broadcast edge.
type edgeClient struct {
	e    *env
	priv ed25519.PrivateKey
}

func (c edgeClient) do(method, path string, body any, at time.Time) resp {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, path, nil)
	edgeh.Sign(c.priv, req, b, at)
	cl := c.e.client()
	cl.header = map[string]string{edgeh.HeaderTimestamp: req.Header.Get(edgeh.HeaderTimestamp), edgeh.HeaderSignature: req.Header.Get(edgeh.HeaderSignature)}
	if body == nil {
		return cl.do(method, path, nil)
	}
	return cl.do(method, path, b)
}

type usageReply struct {
	Taken, Duplicates []string
	Rejected          []map[string]string
	States            []billingpkg.PublisherState
}

func (c edgeClient) post(t *testing.T, windows ...billingpkg.UsageWindow) usageReply {
	t.Helper()
	r := c.do("POST", "/_edge/usage", map[string]any{"edge": "test-edge", "windows": windows}, time.Now())
	if r.code != http.StatusOK {
		t.Fatalf("usage post: %d %s", r.code, r.body)
	}
	var out usageReply
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// T04, T05 and the modes: the edge's windows counted once whatever their order and through a
// 2-hour outage; the modes in order as the month's viewing grows; pay-as-you-go billed hourly,
// clamped to the cap, once, through a provider outage; the notices once each.
func TestBroadcastEdgeUsageAndPAYG(t *testing.T) {
	withBroadcastPrices(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e := newEnvFull(t, &fakeProvider{}, nil, func(c *config.Config) {
		c.Broadcast.Enabled = true
		c.Broadcast.PlansEnforce = true // the harness's config starts from zero values, not the defaults
		c.Broadcast.EdgePublicKey = base64.StdEncoding.EncodeToString(pub)
		c.Broadcast.KeyPath = ingestKeyFile(t) // broadcasting on signs ingest tokens (50.21)
	})
	edge := edgeClient{e: e, priv: priv}
	owner := e.registerVerified("caster@example.test")
	uid := e.userID("caster@example.test")
	h := claimHome(e, owner, "caster")
	setPlan(e, uid, billingpkg.PlanPro)
	e.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
	e.exec(`INSERT INTO subscriptions (user_id, provider, plan, status, provider_subscription_id, provider_customer_id)
            VALUES ($1, 'testpay', 'pro', 'active', 'sub_c', 'cus_c')`, uid)

	// the signature: another key, an old timestamp, an altered body
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if r := (edgeClient{e: e, priv: other}).do("POST", "/_edge/usage", map[string]any{"windows": []any{}}, time.Now()); r.code != http.StatusUnauthorized {
		t.Fatalf("another key: %d", r.code)
	}
	if r := edge.do("POST", "/_edge/usage", map[string]any{"windows": []any{}}, time.Now().Add(-10*time.Minute)); r.code != http.StatusUnauthorized {
		t.Fatalf("an old timestamp: %d", r.code)
	}
	if r := edge.do("GET", "/_edge/publishers/"+h.id, nil, time.Now()); r.code != http.StatusOK {
		t.Fatalf("a publisher's state: %d %s", r.code, r.body)
	}

	// T04: a window posted twice counts once; out of order; a 2-hour outage's queue
	base := time.Now().UTC().Truncate(billingpkg.WindowLength).Add(-3 * time.Hour)
	win := func(i int, full, lowest int64) billingpkg.UsageWindow {
		return billingpkg.UsageWindow{Publisher: h.id, Window: base.Add(time.Duration(i) * billingpkg.WindowLength), MinutesFull: full, MinutesLowest: lowest}
	}
	if out := edge.post(t, win(0, 100, 0)); len(out.Taken) != 1 || len(out.States) != 1 || out.States[0].Mode != "full" {
		t.Fatalf("first window: %+v", out)
	}
	if out := edge.post(t, win(0, 100, 0)); len(out.Duplicates) != 1 || len(out.Taken) != 0 {
		t.Fatalf("the same window again: %+v", out)
	}
	if out := edge.post(t, win(2, 10, 0), win(1, 10, 5)); len(out.Taken) != 2 {
		t.Fatalf("out of order: %+v", out)
	}
	var queue []billingpkg.UsageWindow
	for i := 3; i < 27; i++ { // 2 hours of windows, held by the edge while the API was down
		queue = append(queue, win(i, 7, 1))
	}
	queue = append(queue, win(5, 7, 1)) // and one it had sent before the outage, again
	out := edge.post(t, queue...)
	if len(out.Taken) != 24 || len(out.Duplicates) != 1 {
		t.Fatalf("the outage's queue: %d taken, %d again", len(out.Taken), len(out.Duplicates))
	}
	u := out.States[0].Usage
	if u.MinutesFull != 100+10+10+24*7 || u.MinutesLowest != 5+24 || u.MinutesPAYG != 0 {
		t.Fatalf("the month's total: %+v", u)
	}
	if out := edge.post(t, billingpkg.UsageWindow{Publisher: "00000000-0000-0000-0000-000000000000", Window: base},
		billingpkg.UsageWindow{Publisher: h.id, Window: base.Add(time.Minute)}); len(out.Rejected) != 2 {
		t.Fatalf("unknown publisher and a bad window: %+v", out)
	}

	// the modes in order: at the allowance, lowest; at 2x, wait; nobody is told to stop (the
	// edge keeps playing sessions in their mode)
	state := func() billingpkg.PublisherState {
		r := edge.do("GET", "/_edge/publishers/"+h.id, nil, time.Now())
		var st billingpkg.PublisherState
		if r.code != http.StatusOK || json.Unmarshal(r.body, &st) != nil {
			t.Fatalf("state: %d %s", r.code, r.body)
		}
		return st
	}
	e.exec(`UPDATE broadcast_usage_monthly SET minutes_full = 300000, minutes_lowest = 0 WHERE publisher_id = $1`, h.id)
	if st := state(); st.Mode != "lowest" || st.AllowanceMinutes != 300000 || st.FairUseMinutes != 600000 {
		t.Fatalf("at 5,000 h: %+v", st)
	}
	e.exec(`UPDATE broadcast_usage_monthly SET minutes_lowest = 300000 WHERE publisher_id = $1`, h.id)
	if st := state(); st.Mode != "wait" {
		t.Fatalf("at 10,000 h: %+v", st)
	}

	// T05: pay-as-you-go on with a cap of 20 (2,000 cents at 6.00 a 100 h: 20,000 minutes)
	e.exec(`UPDATE broadcast_usage_monthly SET minutes_lowest = 0 WHERE publisher_id = $1`, h.id)
	e.exec(`INSERT INTO broadcast_payg_settings (server_id, enabled, cap_cents) VALUES ($1, true, 2000)`, h.id)
	if st := state(); st.Mode != "full" || !st.PAYG.On {
		t.Fatalf("pay-as-you-go with room: %+v", st)
	}
	var paygWindows []billingpkg.UsageWindow
	for i := 30; i < 35; i++ {
		paygWindows = append(paygWindows, win(i, 5000, 0)) // 25,000 minutes, 5,000 past the cap
	}
	out = edge.post(t, paygWindows...)
	if st := out.States[0]; st.Usage.MinutesPAYG != 20000 || st.Mode != "lowest" || st.PAYG.SpentCents != 2000 {
		t.Fatalf("past the cap: %+v", st)
	}
	notified := map[string]int{}
	var mu sync.Mutex
	store := &billingpkg.BroadcastStore{DB: e.pool.DB, Enabled: true, Enforce: true}
	rep := &billingpkg.BroadcastReporter{Store: store, Provider: e.pay, Enabled: true,
		Notify: func(_ context.Context, server, kind string) error {
			mu.Lock()
			defer mu.Unlock()
			if server == h.id {
				notified[kind]++
			}
			return nil
		}}
	ctx := context.Background()
	month := time.Now()
	e.pay.billing.Down = true
	if n, err := rep.Enqueue(ctx, month); err != nil || n != 1 {
		t.Fatalf("queued %d (%v), want 1", n, err)
	}
	if n, err := rep.Send(ctx); err == nil || n != 0 {
		t.Fatalf("sent %d through the provider's outage (%v)", n, err)
	}
	e.pay.billing.Down = false
	if n, err := rep.Send(ctx); err != nil || n != 1 {
		t.Fatalf("sent %d after the outage (%v)", n, err)
	}
	if n, _ := rep.Enqueue(ctx, month); n != 0 {
		t.Fatalf("queued again: %d", n)
	}
	reports := e.pay.billing.BroadcastReports()
	if len(reports) != 1 {
		t.Fatalf("reports: %v", reports)
	}
	for id, r := range reports {
		// 20,000 minutes are 333 whole hours: never more than the cap's worth (2,000 cents)
		if r.Hours != 333 || r.SubscriptionID != "sub_c" || r.ServerID != h.id || id != billingpkg.BroadcastReportID(h.id, month, time.Now().Truncate(time.Hour)) {
			t.Fatalf("report %s: %+v", id, r)
		}
	}
	if n, err := rep.Reconcile(ctx); err != nil || n != 0 {
		t.Fatalf("reconcile: %d mismatches (%v)", n, err)
	}
	// the notices, once each
	for range 2 {
		if err := rep.Notices(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var kinds []string
	for k, n := range notified {
		if n != 1 {
			t.Errorf("notice %s sent %d times", k, n)
		}
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if !slices.Equal(kinds, []string{"allowance_100", "allowance_80", "cap_100", "cap_80"}) {
		t.Fatalf("notices: %v", kinds)
	}

	// the rollback: plans not enforced, full up to the viewer limit; counting goes on
	lax := &billingpkg.BroadcastStore{DB: e.pool.DB, Enabled: true, Enforce: false}
	if st, err := lax.State(ctx, h.id); err != nil || st.Mode != "full" {
		t.Fatalf("not enforced: %+v %v", st, err)
	}
	// a suspended account is off
	e.exec(`UPDATE users SET status = 'suspended' WHERE id = $1`, uid)
	if st := state(); st.Mode != "off" || st.Plan != "" {
		t.Fatalf("suspended: %+v", st)
	}
}
