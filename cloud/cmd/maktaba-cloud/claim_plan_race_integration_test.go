//go:build integration

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// lockWaiters counts this database's backends waiting on a lock in a statement like pattern.
func (e *env) lockWaiters(pattern string) int {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
        AND wait_event_type = 'Lock' AND query LIKE $1`, pattern)
}

// waitBlockedOrDone waits until a backend waits on a lock in a statement like pattern, or until
// done has a value (the request did not wait), and reports whether it waited.
func (e *env) waitBlockedOrDone(pattern string, done chan resp) bool {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if e.lockWaiters(pattern) > 0 {
			return true
		}
		if len(done) > 0 {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("no backend waits in %q and the request has not answered", pattern)
	return false
}

// A claim and a plan change of its owner at once, as two real requests whose interleaving is
// fixed by a third transaction's row locks, not timed. Whichever is first, the new home ends
// with the owner's new plan and extra streams.
//
//   - writer first: the change is stopped inside its UPDATE of the owner's homes (a sibling
//     home's row is held), after that statement's snapshot, which can't see a home inserted now.
//     The claim must wait for it. (Before the lock the claim went through, and the home kept the
//     old plan, or the new plan with the old extra streams.)
//   - claim first: the claim is stopped inside its INSERT (the slug is held by an uncommitted
//     row), after it read the plan. The change must wait for it, then update its home too.
//     (Before, the change committed and the claim then inserted the old plan.)
func TestClaimRacingAPlanChange(t *testing.T) {
	type change struct {
		name        string
		plan        string // before
		extra       int
		wantPlan    string
		wantExtra   int
		do          func(e *env, admin *client, uid string) resp
		updatesLike string // the writer's first UPDATE of the owner's homes
	}
	webhook := func(kind, status, plan string, extra int) func(*env, *client, string) resp {
		return func(e *env, _ *client, uid string) resp {
			ev := subEvent("evt_"+uid[:8]+kind, kind, "sub_"+uid[:8], "cus_"+uid[:8], uid, status, plan, time.Now().Add(time.Minute), time.Now().Add(720*time.Hour))
			ev.ExtraStreams = extra
			body, sig := signedEvent(ev)
			return e.client().webhook("testpay", body, sig)
		}
	}
	admin := func(plan string) func(*env, *client, string) resp {
		return func(_ *env, a *client, uid string) resp {
			return a.do("POST", "/v1/admin/accounts/"+uid+"/plan", map[string]string{"plan": plan, "reason": "race"})
		}
	}
	changes := []change{
		{"webhook upgrade with extra streams", "free", 0, "pro", 2, webhook("subscription_changed", "active", "pro", 2), "UPDATE servers SET extra_streams%"},
		{"webhook downgrade", "pro", 2, "basic", 0, webhook("subscription_changed", "active", "basic", 0), "UPDATE servers SET extra_streams%"},
		{"webhook cancel", "pro", 2, "free", 0, webhook("subscription_cancelled", "canceled", "pro", 0), "UPDATE servers SET extra_streams%"},
		{"webhook extra streams only", "pro", 0, "pro", 3, webhook("subscription_changed", "active", "pro", 3), "UPDATE servers SET extra_streams%"},
		{"admin upgrade", "free", 0, "basic", 0, admin("basic"), "UPDATE servers SET plan%"},
		{"admin downgrade", "basic", 0, "free", 0, admin("free"), "UPDATE servers SET plan%"},
	}
	for _, ch := range changes {
		for _, writerFirst := range []bool{true, false} {
			name := ch.name + "/claim first"
			if writerFirst {
				name = ch.name + "/writer first"
			}
			t.Run(name, func(t *testing.T) {
				e := newPaidEnv(t)
				withPrices(t)
				ctx := context.Background()
				var op *client
				if strings.HasPrefix(ch.name, "admin") {
					op = e.serviceAdmin("op@example.test")
				}
				owner := e.registerVerified("race@example.test")
				uid := e.userID("race@example.test")
				e.registerVerified("other@example.test")
				otherID := e.userID("other@example.test")
				e.exec(`UPDATE users SET plan = $2 WHERE id = $1`, uid, ch.plan)
				// the admin path refuses a plan a provider bills; the webhook path takes any
				provider := "testpay"
				if op != nil {
					provider = "manual"
				}
				e.exec(`INSERT INTO subscriptions (user_id, provider, plan, status, extra_streams) VALUES ($1, $2, $3, 'active', $4)`,
					uid, provider, ch.plan, ch.extra)
				// the owner already has a home: its row is where the writer is stopped
				e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan, extra_streams) VALUES ($1, 'Old', 'race-old', 'x', $2, $3)`,
					uid, ch.plan, ch.extra)
				code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)

				hold, err := e.pool.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = hold.Rollback() }()
				writer, claim := make(chan resp, 1), make(chan resp, 1)
				agent := e.client()
				startWriter := func() { go func() { writer <- ch.do(e, op, uid) }() }
				startClaim := func() {
					go func() {
						claim <- agent.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "New", "slug": "race-new"})
					}()
				}
				const claimWaits = "%FROM users WHERE id = $1 FOR NO KEY UPDATE%"
				if writerFirst {
					if _, err := hold.ExecContext(ctx, `SELECT 1 FROM servers WHERE slug = 'race-old' FOR UPDATE`); err != nil {
						t.Fatal(err)
					}
					startWriter()
					if !e.waitBlockedOrDone(ch.updatesLike, writer) {
						t.Fatal("the plan change did not stop at the held home")
					}
					startClaim()
					if !e.waitBlockedOrDone(claimWaits, claim) {
						t.Error("the claim did not wait for the plan change")
					}
				} else {
					if _, err := hold.ExecContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'X', 'race-new', 'x')`, otherID); err != nil {
						t.Fatal(err)
					}
					startClaim()
					if !e.waitBlockedOrDone("%INSERT INTO servers%", claim) {
						t.Fatal("the claim did not stop at the held slug")
					}
					startWriter()
					if !e.waitBlockedOrDone(claimWaits, writer) {
						t.Error("the plan change did not wait for the claim")
					}
				}
				if err := hold.Rollback(); err != nil {
					t.Fatal(err)
				}
				if r := <-writer; r.code/100 != 2 {
					t.Fatalf("plan change: %d %s", r.code, r.body)
				}
				if r := <-claim; r.code != http.StatusCreated {
					t.Fatalf("claim: %d %s", r.code, r.body)
				}
				for _, slug := range []string{"race-old", "race-new"} {
					var plan string
					var extra int
					if err := e.pool.DB.QueryRowContext(ctx, `SELECT plan, extra_streams FROM servers WHERE slug = $1`, slug).Scan(&plan, &extra); err != nil {
						t.Fatal(err)
					}
					if plan != ch.wantPlan || extra != ch.wantExtra {
						t.Errorf("%s has %s and %d extra streams, its owner %s and %d", slug, plan, extra, ch.wantPlan, ch.wantExtra)
					}
				}
			})
		}
	}
}

// A suspended owner's claim code is refused as an invalid one is (the same answer, nothing that
// says the account exists or is suspended), it is not spent, and a suspension under way when the
// code is redeemed wins.
func TestClaimOfASuspendedOwnerIsRefused(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("susp@example.test")
	uid := e.userID("susp@example.test")
	mint := func() string {
		return owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)
	}
	redeem := func(code, slug string) resp {
		return e.client().do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "H", "slug": slug})
	}
	code := mint()
	e.exec(`UPDATE users SET status = 'suspended', suspended_at = now() WHERE id = $1`, uid)
	bad := redeem("AAAAAAAA", "susp-home")
	got := redeem(code, "susp-home")
	if got.code != http.StatusNotFound || got.code != bad.code || string(got.body) != string(bad.body) {
		t.Fatalf("suspended owner: %d %s; an unknown code: %d %s", got.code, got.body, bad.code, bad.body)
	}
	if n := e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1`, uid); n != 0 {
		t.Fatalf("%d homes claimed for a suspended owner", n)
	}
	if n := e.count(`SELECT count(*) FROM server_claims WHERE user_id = $1 AND used_at IS NULL`, uid); n != 1 {
		t.Fatal("the refused code was spent")
	}
	// reinstated: the same code works
	e.exec(`UPDATE users SET status = 'active', suspended_at = NULL WHERE id = $1`, uid)
	if r := redeem(code, "susp-home"); r.code != http.StatusCreated {
		t.Fatalf("reinstated owner: %d %s", r.code, r.body)
	}

	// suspended while the claim is under way
	code = mint()
	tx, err := e.pool.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET status = 'suspended', suspended_at = now() WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	done := make(chan resp, 1)
	agent := e.client()
	go func() {
		done <- agent.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "H", "slug": "susp-home-2"})
	}()
	if !e.waitBlockedOrDone("%FROM users WHERE id = $1 FOR NO KEY UPDATE%", done) {
		t.Error("the claim did not wait for the suspension")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.code != http.StatusNotFound || r.errCode() != "claim_invalid" {
		t.Fatalf("suspended meanwhile: %d %s", r.code, r.body)
	}
}

// A console plan change that waited for another one records the plan it replaced, not the one
// its request saw before the wait.
func TestPlanChangeAuditsThePlanItReplaced(t *testing.T) {
	e := newPaidEnv(t)
	admin := e.serviceAdmin("op@example.test")
	e.registerVerified("audit@example.test")
	uid := e.userID("audit@example.test")
	tx, err := e.pool.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET plan = 'pro' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	done := make(chan resp, 1)
	go func() {
		done <- admin.do("POST", "/v1/admin/accounts/"+uid+"/plan", map[string]string{"plan": "basic", "reason": "second"})
	}()
	if !e.waitBlockedOrDone("%FROM users WHERE id = $1 FOR NO KEY UPDATE%", done) {
		t.Fatal("the second change did not wait")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.code != http.StatusNoContent {
		t.Fatalf("%d %s", r.code, r.body)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.account.set_plan' AND target_id = $1
        AND detail->>'previous_plan' = 'pro' AND detail->>'plan' = 'basic'`, uid); n != 1 {
		t.Fatal("the audit does not name the plan replaced")
	}
}
