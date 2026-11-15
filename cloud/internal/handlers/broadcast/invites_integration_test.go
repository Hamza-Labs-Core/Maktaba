//go:build integration

package broadcast

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	billingh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// An invite created while a plan change that ends guest sharing is in flight can't slip past
// the revocation (story 50.20's edge cases). The interleaving is fixed, not timed: the plan
// change's transaction runs SetEffectivePlan and holds its row locks; the create, which already
// read the old plan, then waits on the home's row lock (seen in pg_stat_activity), and the plan
// change commits. The create must read the plan again under that lock and refuse; before that
// re-check it inserted a live invite on a Free home (201).
func TestInviteCreateRacingAPlanChange(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "R", true)
	if err != nil {
		t.Fatal(err)
	}
	var home string
	if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET status = 'active', plan = 'pro' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan, broadcast_test_publisher)
        VALUES ($1, 'r', $2, 'x', 'pro', true) RETURNING id`, u.ID, "r"+uuid.NewString()[:8]).Scan(&home); err != nil {
		t.Fatal(err)
	}
	var seed [32]byte
	key, err := bc.KeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{DB: pool.DB, Store: &billingpkg.BroadcastStore{DB: pool.DB}, Key: &key}

	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := billingh.SetEffectivePlan(ctx, tx, u.ID, billingpkg.PlanFree); err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		d.CreateInvite(w, httptest.NewRequest(http.MethodPost, "/v1/broadcast/invites", strings.NewReader(`{}`)), actor{server: home})
		done <- w
	}()
	// the create has passed its first plan check (Pro: the plan change hasn't committed) and
	// waits for the home's row, which the plan change holds
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
            WHERE datname = current_database() AND wait_event_type = 'Lock'
              AND query LIKE '%FROM servers WHERE id = $1 FOR UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case w := <-done:
			t.Fatalf("the create finished without waiting for the plan change: %d %s", w.Code, w.Body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the create never waited on the home's row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	w := <-done
	if w.Code != http.StatusPaymentRequired || !strings.Contains(w.Body.String(), "broadcast_needs_plan") {
		t.Fatalf("an invite made across the plan's end: %d %s", w.Code, w.Body)
	}
	var live int
	if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_invites WHERE server_id = $1 AND revoked_at IS NULL`, home).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("a Free home has %d live invites", live)
	}
}

// A channel set to open while a plan change that ends open access is in flight can't stay open
// once the plan has ended, whether a channel is switched to open (SetChannelAccess) or published
// open (Publish). The interleaving is fixed as in TestInviteCreateRacingAPlanChange: the plan
// change holds its row locks, the request (which already read Broadcaster) waits on the home's
// row, and the plan change commits. The request must read the plan again under that lock and
// refuse; before that re-check the switch set the channel open after SetEffectivePlan had set the
// home's channels back to invite (204), and the publish inserted an open channel (201).
func TestOpenAccessRacingAPlanChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(d *Deps, home, secret, ch string) *httptest.ResponseRecorder
	}{
		{"switch to open", func(d *Deps, home, _, ch string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPatch, "/v1/broadcast/channels/"+ch, strings.NewReader(`{"access":"open"}`))
			rc := chi.NewRouteContext()
			rc.URLParams.Add("id", ch)
			d.SetChannelAccess(w, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc)), actor{server: home})
			return w
		}},
		{"publish open", func(d *Deps, home, secret, _ string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/broadcast/channels", strings.NewReader(`{"slug":"second","access":"open"}`))
			req.SetBasicAuth(home, secret)
			d.Publish(w, req)
			return w
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := dbtest.Migrated(t)
			u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "O", true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET status = 'active', plan = 'pro', email_verified = true WHERE id = $1`, u.ID); err != nil {
				t.Fatal(err)
			}
			const secret = "home-secret"
			hash, err := argon2id.Hash(secret, argon2id.DefaultParams())
			if err != nil {
				t.Fatal(err)
			}
			var home, ch string
			if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan, broadcast_plan, broadcast_test_publisher)
                VALUES ($1, 'o', $2, $3, 'pro', 'broadcaster', true) RETURNING id`, u.ID, "o"+uuid.NewString()[:8], hash).Scan(&home); err != nil {
				t.Fatal(err)
			}
			// verified for open: a paid plan, a confirmed email, a first broadcast 8 days ago
			if err := pool.DB.QueryRowContext(ctx, `INSERT INTO broadcast_channels (server_id, slug, access, agreement_version, published_at)
                VALUES ($1, 'first', 'invite', $2, now() - interval '8 days') RETURNING id::text`, home, billingpkg.BroadcastAgreementVersion).Scan(&ch); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.DB.ExecContext(ctx, `INSERT INTO broadcast_agreements (account_id, version) VALUES ($1, $2)`, u.ID, billingpkg.BroadcastAgreementVersion); err != nil {
				t.Fatal(err)
			}
			var seed [32]byte
			key, err := bc.KeyFromSeed(seed[:])
			if err != nil {
				t.Fatal(err)
			}
			d := &Deps{DB: pool.DB, Store: &billingpkg.BroadcastStore{DB: pool.DB}, Servers: stores.NewServers(pool.DB), Key: &key, OpenAccess: true}

			tx, err := pool.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := billingh.SetEffectivePlan(ctx, tx, u.ID, billingpkg.PlanFree); err != nil {
				t.Fatal(err)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- tc.run(d, home, secret, ch) }()
			// the request passed its first plan check (the plan change hasn't committed) and waits
			// on the home's row, which the plan change holds (before the fix the switch waited on
			// the access version's update of that row, after setting the channel open)
			awaitLockOnServers(t, pool.DB, done)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if w := <-done; w.Code < 400 {
				t.Fatalf("open access set across the plan's end: %d %s", w.Code, w.Body)
			}
			var open int
			if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_channels WHERE server_id = $1 AND access = 'open'`, home).Scan(&open); err != nil {
				t.Fatal(err)
			}
			if open != 0 {
				t.Fatalf("a Free home has %d open channels", open)
			}
		})
	}
}

// awaitLockOnServers waits until a request is blocked on a row lock in a statement on servers,
// failing if the request finishes first or never waits.
func awaitLockOnServers(t *testing.T, db *sql.DB, done chan *httptest.ResponseRecorder) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity
            WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%servers%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		select {
		case w := <-done:
			t.Fatalf("the request finished without waiting for the plan change: %d %s", w.Code, w.Body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the request never waited on the home's row")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
