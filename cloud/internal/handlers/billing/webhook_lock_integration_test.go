//go:build integration

package billing

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// A subscription event for an account whose purge commits while the event waits for the
// account's lock is acknowledged with no work (no error, so the provider does not retry it
// for ever), and leaves nothing behind.
func TestSubscriptionEventOfAnAccountPurgedMeanwhile(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "P", true)
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{DB: pool.DB, Users: stores.NewUsers(pool.DB), Logger: slog.New(slog.DiscardHandler)}
	purge, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = purge.Rollback() }()
	if _, err := purge.Exec(`DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		notices int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		tx, err := pool.DB.BeginTx(ctx, nil)
		if err != nil {
			done <- result{err: err}
			return
		}
		n, err := d.applyEvent(ctx, tx, "testpay", billingpkg.Event{ID: "e", Kind: billingpkg.EventSubscriptionStarted, At: time.Now(),
			UserID: u.ID, CustomerID: "cus", SubscriptionID: "sub", Plan: billingpkg.PlanBasic, Status: "active"})
		if err == nil {
			err = tx.Commit()
		}
		done <- result{len(n), err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for waiting := 0; waiting == 0; time.Sleep(10 * time.Millisecond) {
		if err := pool.DB.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
            AND wait_event_type = 'Lock' AND query LIKE '%FROM users WHERE id = $1 FOR NO KEY UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the event did not wait for the account's lock")
		}
	}
	if err := purge.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.err != nil || r.notices != 0 {
		t.Fatalf("%d notices, %v", r.notices, r.err)
	}
	var left int
	if err := pool.DB.QueryRow(`SELECT count(*) FROM subscriptions`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d subscriptions left (%v)", left, err)
	}
}

// A failed payment's notice is in the account's language, with the billing page's link.
func TestThePaymentNoticeIsInTheAccountsLanguage(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	users := stores.NewUsers(pool.DB)
	rec := &mailpkg.Recorder{}
	d := &Deps{DB: pool.DB, Users: users, Mailer: rec, Logger: slog.New(slog.DiscardHandler), WebURL: "https://app.example.test"}
	for _, lang := range []string{"en", "ar"} {
		email := lang + "@example.test"
		u, err := users.Create(ctx, email, "", "P", true)
		if err != nil {
			t.Fatal(err)
		}
		if err := users.UpdateProfile(ctx, u.ID, "", lang, ""); err != nil {
			t.Fatal(err)
		}
		d.mailUser(u.ID, "payment_failed")(ctx)
		m, ok := rec.Last("payment_failed", email)
		if !ok || m.Lang != lang || !strings.Contains(m.Body, "https://app.example.test/billing\n") {
			t.Fatalf("%s: %+v", lang, m)
		}
		d.mailUser(u.ID, "no such notice")(ctx)
	}
	if len(rec.Sent) != 2 {
		t.Fatalf("%d mails", len(rec.Sent))
	}
}
