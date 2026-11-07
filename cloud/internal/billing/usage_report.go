package billing

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Reporter sends the pay-as-you-go GB to the payment provider (story 50.19 §6): every hour it
// queues each home's whole GB not reported yet, clamped so an account is never billed past its
// cap, then sends what is queued with each report's ID as the provider's idempotency key. A
// provider that is down leaves them queued; they go later under the same IDs, so usage is never
// lost and never billed twice. It also sends the 80 % and 100 % notices, and reconciles daily.
type Reporter struct {
	DB       *sql.DB
	Provider Provider
	Logger   *slog.Logger
	// Notify tells an account it used 80 % (near true) or all of its cap this month.
	Notify func(ctx context.Context, uid string, near bool) error
	Now    func() time.Time
}

func (r *Reporter) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// ReportID is a report's idempotency key: <server_id>:<month>:<hour>.
func ReportID(serverID string, month, hour time.Time) string {
	return fmt.Sprintf("%s:%s:%s", serverID, MonthStart(month).Format("2006-01"), hour.UTC().Format("2006-01-02T15"))
}

type paygAccount struct {
	uid, sub, customer string
	capCents           int64
}

// accounts are the accounts with pay-as-you-go on and a live paid subscription.
func (r *Reporter) accounts(ctx context.Context) ([]paygAccount, error) {
	rows, err := r.DB.QueryContext(ctx, `
        SELECT g.user_id, COALESCE(s.provider_subscription_id, ''), COALESCE(s.provider_customer_id, ''), g.cap_cents
          FROM payg_settings g JOIN subscriptions s ON s.user_id = g.user_id
         WHERE g.enabled AND s.plan = $1 AND s.status IN ('active','trialing','past_due')`, PlanPro)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []paygAccount
	for rows.Next() {
		var a paygAccount
		if err := rows.Scan(&a.uid, &a.sub, &a.customer, &a.capCents); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Enqueue queues this hour's reports for month: per home, the whole GB of pay-as-you-go not
// queued yet, with the account's total kept within its cap's worth of GB.
func (r *Reporter) Enqueue(ctx context.Context, month time.Time) (int, error) {
	t := Tiers[PlanPro]
	if t.PAYGPricePerGBCents == nil || *t.PAYGPricePerGBCents <= 0 {
		return 0, nil // no price, nothing billable
	}
	price := int64(*t.PAYGPricePerGBCents)
	accounts, err := r.accounts(ctx)
	if err != nil {
		return 0, err
	}
	hour := r.now().Truncate(time.Hour)
	queued := 0
	for _, a := range accounts {
		allowed := a.capCents / price // the cap's worth in whole GB
		var accountGB int64
		if err := r.DB.QueryRowContext(ctx, `SELECT COALESCE(sum(gb), 0) FROM billing_usage_outbox WHERE user_id = $1 AND month = $2`,
			a.uid, MonthStart(month)).Scan(&accountGB); err != nil {
			return queued, err
		}
		rows, err := r.DB.QueryContext(ctx, `
            SELECT u.server_id, u.bytes_payg,
                   COALESCE((SELECT sum(gb) FROM billing_usage_outbox o WHERE o.server_id = u.server_id AND o.month = u.month), 0)
              FROM relay_usage_monthly u JOIN servers s ON s.id = u.server_id
             WHERE s.owner_user_id = $1 AND u.month = $2 AND u.bytes_payg > 0
             ORDER BY u.server_id`, a.uid, MonthStart(month))
		if err != nil {
			return queued, err
		}
		type due struct {
			server string
			gb     int64
		}
		var dues []due
		for rows.Next() {
			var server string
			var bytes, reported int64
			if err := rows.Scan(&server, &bytes, &reported); err != nil {
				_ = rows.Close()
				return queued, err
			}
			if gb := bytes/GB - reported; gb > 0 {
				dues = append(dues, due{server, gb})
			}
		}
		_ = rows.Close()
		for _, d := range dues {
			gb := min(d.gb, allowed-accountGB) // never past the cap (§4: overshoot is clamped)
			if gb <= 0 {
				break
			}
			res, err := r.DB.ExecContext(ctx, `
                INSERT INTO billing_usage_outbox (id, user_id, server_id, month, gb) VALUES ($1, $2, $3, $4, $5)
                ON CONFLICT (id) DO NOTHING`, ReportID(d.server, month, hour), a.uid, d.server, MonthStart(month), gb)
			if err != nil {
				return queued, err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				accountGB += gb
				queued++
			}
		}
	}
	return queued, nil
}

// Send sends the queued reports, oldest first. A failure stays queued for the next run.
func (r *Reporter) Send(ctx context.Context) (sent int, err error) {
	rows, err := r.DB.QueryContext(ctx, `
        SELECT o.id, o.gb, o.created_at, COALESCE(s.provider_subscription_id, ''), COALESCE(s.provider_customer_id, '')
          FROM billing_usage_outbox o LEFT JOIN subscriptions s ON s.user_id = o.user_id
         WHERE o.sent_at IS NULL ORDER BY o.created_at LIMIT 500`)
	if err != nil {
		return 0, err
	}
	var reports []UsageReport
	for rows.Next() {
		var u UsageReport
		if err := rows.Scan(&u.ID, &u.GB, &u.At, &u.SubscriptionID, &u.CustomerID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		reports = append(reports, u)
	}
	_ = rows.Close()
	for _, u := range reports {
		if perr := r.Provider.ReportUsage(ctx, u); perr != nil {
			_, _ = r.DB.ExecContext(ctx, `UPDATE billing_usage_outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`, u.ID, perr.Error())
			err = perr
			continue
		}
		if _, uerr := r.DB.ExecContext(ctx, `UPDATE billing_usage_outbox SET sent_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1`, u.ID); uerr != nil {
			return sent, uerr
		}
		sent++
	}
	return sent, err
}

// Notices tells each account once a month when its pay-as-you-go reaches 80 % and 100 % of
// its cap (payg.nearCap, payg.capReached).
func (r *Reporter) Notices(ctx context.Context) error {
	t := Tiers[PlanPro]
	if r.Notify == nil || t.PAYGPricePerGBCents == nil || *t.PAYGPricePerGBCents <= 0 {
		return nil
	}
	price := int64(*t.PAYGPricePerGBCents)
	accounts, err := r.accounts(ctx)
	if err != nil {
		return err
	}
	month := MonthStart(r.now())
	store := &PAYGStore{DB: r.DB}
	for _, a := range accounts {
		if a.capCents <= 0 {
			continue
		}
		bytes, err := store.SpentBytes(ctx, a.uid, month)
		if err != nil {
			return err
		}
		spent := PAYGSpentCents(bytes, price)
		var near, capped sql.NullTime
		if err := r.DB.QueryRowContext(ctx, `SELECT near_notified_on, cap_notified_on FROM payg_settings WHERE user_id = $1`, a.uid).Scan(&near, &capped); err != nil {
			return err
		}
		sentThisMonth := func(d sql.NullTime) bool { return d.Valid && !d.Time.Before(month) }
		switch {
		case spent >= a.capCents && !sentThisMonth(capped):
			if err := r.Notify(ctx, a.uid, false); err == nil {
				_, _ = r.DB.ExecContext(ctx, `UPDATE payg_settings SET cap_notified_on = $2, near_notified_on = COALESCE(near_notified_on, $2) WHERE user_id = $1`, a.uid, month)
			}
		case spent*100 >= a.capCents*80 && spent < a.capCents && !sentThisMonth(near):
			if err := r.Notify(ctx, a.uid, true); err == nil {
				_, _ = r.DB.ExecContext(ctx, `UPDATE payg_settings SET near_notified_on = $2 WHERE user_id = $1`, a.uid, month)
			}
		}
	}
	return nil
}

// Reconcile compares, per subscription, the GB sent this month with what the provider
// recorded; a difference over 1 GB is logged as billing_usage_mismatch (a SEV3 alert).
func (r *Reporter) Reconcile(ctx context.Context) (mismatches int, err error) {
	month := MonthStart(r.now())
	rows, err := r.DB.QueryContext(ctx, `
        SELECT COALESCE(s.provider_subscription_id, ''), COALESCE(sum(o.gb), 0)
          FROM billing_usage_outbox o JOIN subscriptions s ON s.user_id = o.user_id
         WHERE o.month = $1 AND o.sent_at IS NOT NULL
         GROUP BY s.provider_subscription_id`, month)
	if err != nil {
		return 0, err
	}
	type sub struct {
		id string
		gb int64
	}
	var subs []sub
	for rows.Next() {
		var s sub
		if err := rows.Scan(&s.id, &s.gb); err != nil {
			_ = rows.Close()
			return 0, err
		}
		subs = append(subs, s)
	}
	_ = rows.Close()
	for _, s := range subs {
		recorded, perr := r.Provider.ReportedUsage(ctx, s.id, month)
		if perr != nil {
			err = perr
			continue
		}
		if d := recorded - s.gb; d > 1 || d < -1 {
			mismatches++
			r.log().Warn("billing: the provider's pay-as-you-go GB differ from ours", "subscription", s.id,
				"ours_gb", s.gb, "provider_gb", recorded, "event", "billing_usage_mismatch")
		}
	}
	return mismatches, err
}

func (r *Reporter) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// Run reports every hour (this month's, and on the 1st the month before too, so its last
// bytes go out before the provider finalises the invoice), sends the notices, and reconciles
// once a day.
func (r *Reporter) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	var lastReconcile time.Time
	for {
		now := r.now()
		months := []time.Time{now}
		if now.Day() == 1 {
			months = append(months, MonthStart(now).AddDate(0, -1, 0))
		}
		for _, m := range months {
			if _, err := r.Enqueue(ctx, m); err != nil {
				r.log().Warn("billing: queueing usage reports", "err", err)
			}
		}
		if n, err := r.Send(ctx); err != nil {
			r.log().Warn("billing: usage reports stay queued", "sent", n, "err", err, "event", "billing_usage_queued")
		}
		if err := r.Notices(ctx); err != nil {
			r.log().Warn("billing: pay-as-you-go notices", "err", err)
		}
		if now.Sub(lastReconcile) >= 24*time.Hour {
			if _, err := r.Reconcile(ctx); err == nil {
				lastReconcile = now
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
