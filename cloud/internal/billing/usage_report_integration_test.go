//go:build integration

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// withProPrices sets Pro's allowance and pay-as-you-go price for a test (they are TBD).
func withProPrices(t *testing.T, allowanceGB int64, paygCents int) {
	t.Helper()
	old := Tiers
	set := map[string]Tier{}
	for k, v := range old {
		set[k] = v
	}
	p := set[PlanPro]
	a := allowanceGB * GB
	p.AllowanceBytesPerMo, p.PAYGPricePerGBCents = &a, &paygCents
	set[PlanPro] = p
	Tiers = set
	t.Cleanup(func() { Tiers = old })
}

// Story 50.19 T07 (and T06's pay-as-you-go part): a Pro account's pay-as-you-go GB are reported
// hourly with idempotency keys; a retry is taken once; a provider outage leaves them queued and
// they go later under the same keys; the account is never billed past its cap; the notices go
// at 80 % and 100 %; the reconciliation sees a mismatch; the meter stops pay-as-you-go at the cap.
func TestUsageReports(t *testing.T) {
	withProPrices(t, 100, 10) // 100 GB allowance, 10 ¢ a GB
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "P", true)
	if err != nil {
		t.Fatal(err)
	}
	var home string
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan)
	        VALUES ($1, 'h', $2, 'x', 'pro') RETURNING id`, u.ID, "h"+uuid.NewString()[:8]).Scan(&home); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO subscriptions (user_id, plan, status, provider_subscription_id) VALUES ($1, 'pro', 'active', 'sub_1')`, u.ID); err != nil {
		t.Fatal(err)
	}
	payg := &PAYGStore{DB: pool.DB}
	if err := payg.Set(ctx, u.ID, PAYGSettings{Enabled: true}); err != ErrPAYGCapRequired {
		t.Fatalf("on without a cap: %v", err)
	}
	if err := payg.Set(ctx, u.ID, PAYGSettings{Enabled: true, CapCents: 100}); err != nil { // 10 GB's worth
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC)
	month := MonthStart(clock)
	use := func(gb int64) {
		if _, err := pool.DB.ExecContext(ctx, `
            INSERT INTO relay_usage_monthly (server_id, month, bytes_full, bytes_payg) VALUES ($1, $2, $3, $4)
            ON CONFLICT (server_id, month) DO UPDATE SET bytes_payg = EXCLUDED.bytes_payg`, home, month, 100*GB, gb*GB); err != nil {
			t.Fatal(err)
		}
	}
	fake := &Fake{}
	var notices []bool
	r := &Reporter{DB: pool.DB, Provider: fake, Now: func() time.Time { return clock },
		Notify: func(_ context.Context, _ string, near bool) error { notices = append(notices, near); return nil }}

	// the meter: within the cap, pay-as-you-go at full quality
	m := NewMeter(pool.DB)
	m.PAYG = payg
	m.now = func() time.Time { return clock }
	sv := stores.Server{ID: home, Plan: PlanPro}
	use(3)
	if d := m.Decide(ctx, sv); d.Mode != ModeFull || !d.PAYG {
		t.Fatalf("within the cap: %+v", d)
	}

	// hour 1: 3 GB queued and sent; sending again is taken once
	if n, err := r.Enqueue(ctx, month); err != nil || n != 1 {
		t.Fatalf("queued %d %v", n, err)
	}
	if n, err := r.Send(ctx); err != nil || n != 1 {
		t.Fatalf("sent %d %v", n, err)
	}
	id := ReportID(home, month, clock.Truncate(time.Hour))
	_ = fake.ReportUsage(ctx, UsageReport{ID: id, SubscriptionID: "sub_1", GB: 3, At: clock}) // a retry
	if rep := fake.Reports(); len(rep) != 1 || rep[id].GB != 3 {
		t.Fatalf("reports %+v", rep)
	}
	// hour 2, the provider down: queued, not lost
	clock = clock.Add(time.Hour)
	use(8)
	fake.Down = true
	if n, _ := r.Enqueue(ctx, month); n != 1 {
		t.Fatalf("hour 2 queued %d", n)
	}
	if _, err := r.Send(ctx); err == nil {
		t.Fatal("sent to a provider that is down")
	}
	if err := r.Notices(ctx); err != nil || len(notices) != 1 || !notices[0] { // 8 GB = 80 ¢ of 100
		t.Fatalf("near notice: %v %v", notices, err)
	}
	// hour 3: back; the queued report goes under its own key, and 25 GB used is clamped to the
	// cap's 10 GB
	clock = clock.Add(time.Hour)
	fake.Down = false
	use(25)
	if _, err := r.Enqueue(ctx, month); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Send(ctx); err != nil || n != 2 {
		t.Fatalf("hour 3 sent %d %v", n, err)
	}
	var total int64
	for _, rep := range fake.Reports() {
		total += rep.GB
	}
	if total != 10 {
		t.Fatalf("billed %d GB, the cap is 10", total)
	}
	if err := r.Notices(ctx); err != nil || len(notices) != 2 || notices[1] {
		t.Fatalf("cap notice: %v %v", notices, err)
	}
	if err := r.Notices(ctx); err != nil || len(notices) != 2 {
		t.Fatalf("a notice twice: %v", notices)
	}
	// the meter: past the cap, capped quality (never refused)
	m2 := NewMeter(pool.DB)
	m2.PAYG = payg
	m2.now = func() time.Time { return clock }
	if d := m2.Decide(ctx, sv); d.Mode != ModeCapped || d.PAYG {
		t.Fatalf("past the cap: %+v", d)
	}
	// reconciliation: agrees, then a provider that recorded 5 GB more is a mismatch
	if n, err := r.Reconcile(ctx); err != nil || n != 0 {
		t.Fatalf("reconcile: %d %v", n, err)
	}
	_ = fake.ReportUsage(ctx, UsageReport{ID: "stray", SubscriptionID: "sub_1", GB: 5, At: clock})
	if n, _ := r.Reconcile(ctx); n != 1 {
		t.Fatalf("a 5 GB mismatch unseen: %d", n)
	}
}
