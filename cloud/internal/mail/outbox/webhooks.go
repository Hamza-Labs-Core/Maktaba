package outbox

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
)

// Reputation thresholds (Resend flags accounts at 4 % hard bounces and 0.08 % complaints;
// alerts trigger when the 30-day rate nears either limit).
const (
	BounceAlertRate    = 0.03
	BounceLimitRate    = 0.04
	ComplaintAlertRate = 0.0006
	ComplaintLimitRate = 0.0008
	// MinBounceSample avoids alerting on a single typo when total 30-day volume is very small
	// (2+ hard bounces at >= BounceAlertRate still alert at any volume).
	MinBounceSample = 20
)

// Suppression reasons stored in mail_suppressions.
const (
	SuppressionHardBounce = "hard_bounce"
	SuppressionComplaint  = "complaint"
)

// EventCounts summarizes provider delivery-failure webhooks over the last 30 days.
type EventCounts struct {
	Bounced             int      `json:"bounced"`
	HardBounced         int      `json:"hard_bounced"`
	Complained          int      `json:"complained"`
	DeliveryDelayed     int      `json:"delivery_delayed"`
	Failed              int      `json:"failed"`
	SuppressedAddresses int      `json:"suppressed_addresses"`
	BounceRate          float64  `json:"bounce_rate"`
	ComplaintRate       float64  `json:"complaint_rate"`
	Alerts              []string `json:"alerts,omitempty"`
}

// EventOutcome describes what RecordEvent did for one webhook delivery.
type EventOutcome struct {
	Duplicate  bool
	Matched    bool
	Suppressed bool
	Alerts     []string
}

// RateAlerts returns the active operator alert codes for the 30-day counts.
func RateAlerts(sent30d, hardBounced, complained int) []string {
	if sent30d <= 0 {
		return nil
	}
	var alerts []string
	bounceRate := float64(hardBounced) / float64(sent30d)
	if bounceRate >= BounceAlertRate && (hardBounced >= 2 || sent30d >= MinBounceSample) {
		alerts = append(alerts, mail.AlertBounceRate)
	}
	complaintRate := float64(complained) / float64(sent30d)
	if complained > 0 && complaintRate >= ComplaintAlertRate {
		alerts = append(alerts, mail.AlertComplaintRate)
	}
	return alerts
}

func roundRate(num, den int) float64 {
	if den <= 0 || num <= 0 {
		return 0
	}
	return math.Round(float64(num)/float64(den)*1e6) / 1e6
}

func (o *Outbox) isSuppressed(ctx context.Context, tx *sql.Tx, hash string) (string, bool, error) {
	if hash == "" {
		return "", false, nil
	}
	var reason string
	err := tx.QueryRowContext(ctx, `SELECT reason FROM mail_suppressions WHERE to_hash = $1`, hash).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return reason, true, nil
}

// RecordEvent records one verified Resend webhook event idempotently, matches it to mail_outbox by
// provider_id (never by a raw address), suppresses the address hash on hard bounce or complaint,
// and logs an operator alert when 30-day rates near Resend's limits.
func (o *Outbox) RecordEvent(ctx context.Context, ev mail.WebhookEvent) (EventOutcome, error) {
	if ev.EventID == "" || !mail.TrackedWebhookEvent(ev.Type) {
		return EventOutcome{}, nil
	}
	now := o.now().UTC()
	occurred := ev.OccurredAt.UTC()
	if occurred.IsZero() {
		occurred = now
	}

	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return EventOutcome{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var outboxID sql.NullString
	var toHash string
	if ev.EmailID != "" {
		var id string
		err := tx.QueryRowContext(ctx,
			`SELECT id::text, to_hash FROM mail_outbox WHERE provider_id = $1 ORDER BY created_at DESC LIMIT 1`,
			ev.EmailID).Scan(&id, &toHash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return EventOutcome{}, err
		}
		if err == nil {
			outboxID = sql.NullString{String: id, Valid: true}
		}
	}

	res, err := tx.ExecContext(ctx, `
        INSERT INTO mail_events (event_id, provider_id, outbox_id, to_hash, event_type, bounce_type, occurred_at, received_at)
        VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8)
        ON CONFLICT (event_id) DO NOTHING`,
		ev.EventID, ev.EmailID, outboxID.String, toHash, ev.Type, ev.BounceType, occurred, now)
	if err != nil {
		return EventOutcome{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return EventOutcome{Duplicate: true}, nil
	}

	var suppressed bool
	var suppressReason string
	switch {
	case ev.Type == mail.EventComplained:
		suppressReason = SuppressionComplaint
	case ev.Type == mail.EventBounced && ev.BounceType == mail.BounceHard:
		suppressReason = SuppressionHardBounce
	}
	if suppressReason != "" && toHash != "" {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO mail_suppressions (to_hash, reason, event_id, created_at)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (to_hash) DO UPDATE
               SET reason = CASE WHEN mail_suppressions.reason = 'complaint' THEN mail_suppressions.reason ELSE EXCLUDED.reason END,
                   event_id = EXCLUDED.event_id`,
			toHash, suppressReason, ev.EventID, now); err != nil {
			return EventOutcome{}, err
		}
		suppressed = true
	}

	if err := tx.Commit(); err != nil {
		return EventOutcome{}, err
	}

	out := EventOutcome{Matched: outboxID.Valid, Suppressed: suppressed}
	if o.Logger != nil {
		o.Logger.Warn("mail: provider delivery event",
			"event", webhookLogEvent(ev.Type),
			"event_id", ev.EventID,
			"mail", outboxID.String,
			"bounce_type", ev.BounceType,
			"matched", out.Matched,
			"suppressed", out.Suppressed)
	}

	if ev.Type == mail.EventBounced || ev.Type == mail.EventComplained {
		counts, err := Count(ctx, o.DB)
		if err == nil {
			out.Alerts = counts.Events.Alerts
			for _, code := range out.Alerts {
				if o.Logger != nil {
					o.Logger.Error("mail: delivery reputation rate nears Resend limit",
						"code", code,
						"event", code,
						"sent_30d", counts.Sent30d,
						"hard_bounced_30d", counts.Events.HardBounced,
						"complained_30d", counts.Events.Complained,
						"bounce_rate", counts.Events.BounceRate,
						"complaint_rate", counts.Events.ComplaintRate)
				}
			}
		}
	}
	return out, nil
}

func webhookLogEvent(eventType string) string {
	switch eventType {
	case mail.EventBounced:
		return "mail_bounced"
	case mail.EventComplained:
		return "mail_complained"
	case mail.EventDeliveryDelayed:
		return "mail_delivery_delayed"
	case mail.EventFailed:
		return "mail_delivery_failed"
	default:
		return "mail_webhook_event"
	}
}

func countEvents(ctx context.Context, db *sql.DB, sent30d int) (EventCounts, error) {
	var ec EventCounts
	err := db.QueryRowContext(ctx, `
        SELECT
            count(*) FILTER (WHERE event_type = 'email.bounced'),
            count(*) FILTER (WHERE event_type = 'email.bounced' AND bounce_type = 'hard'),
            count(*) FILTER (WHERE event_type = 'email.complained'),
            count(*) FILTER (WHERE event_type = 'email.delivery_delayed'),
            count(*) FILTER (WHERE event_type = 'email.failed')
          FROM mail_events
         WHERE received_at > now() - INTERVAL '30 days'`).
		Scan(&ec.Bounced, &ec.HardBounced, &ec.Complained, &ec.DeliveryDelayed, &ec.Failed)
	if err != nil {
		return ec, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mail_suppressions`).Scan(&ec.SuppressedAddresses); err != nil {
		return ec, err
	}
	ec.BounceRate = roundRate(ec.HardBounced, sent30d)
	ec.ComplaintRate = roundRate(ec.Complained, sent30d)
	ec.Alerts = RateAlerts(sent30d, ec.HardBounced, ec.Complained)
	return ec, nil
}
