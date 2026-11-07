package billing

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PAYGStore is the accounts' pay-as-you-go settings and what they spent (story 50.19 §5). The
// cap is the account's, across its homes; spending is the pay-as-you-go bytes of all of them at
// Pro's price a GB.
type PAYGStore struct {
	DB *sql.DB
}

// PAYGSettings are an account's.
type PAYGSettings struct {
	Enabled  bool  `json:"enabled"`
	CapCents int64 `json:"cap_cents"`
}

// ErrPAYGCapRequired: pay-as-you-go can't be on without a monthly cap.
var ErrPAYGCapRequired = errors.New("billing: set a monthly spending limit to turn on pay as you go")

// Get is an account's settings (off when it has none).
func (s *PAYGStore) Get(ctx context.Context, uid string) (PAYGSettings, error) {
	var p PAYGSettings
	err := s.DB.QueryRowContext(ctx, `SELECT enabled, cap_cents FROM payg_settings WHERE user_id = $1`, uid).Scan(&p.Enabled, &p.CapCents)
	if errors.Is(err, sql.ErrNoRows) {
		return PAYGSettings{}, nil
	}
	return p, err
}

// Set keeps an account's settings: on needs a cap above zero. A cap lowered below what is
// spent stops pay-as-you-go at once (what was spent stays billed).
func (s *PAYGStore) Set(ctx context.Context, uid string, p PAYGSettings) error {
	if p.Enabled && p.CapCents <= 0 {
		return ErrPAYGCapRequired
	}
	if p.CapCents < 0 {
		p.CapCents = 0
	}
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO payg_settings (user_id, enabled, cap_cents, updated_at) VALUES ($1, $2, $3, now())
        ON CONFLICT (user_id) DO UPDATE SET enabled = EXCLUDED.enabled, cap_cents = EXCLUDED.cap_cents, updated_at = now()`,
		uid, p.Enabled, p.CapCents)
	return err
}

// SpentBytes is an account's pay-as-you-go bytes in month, across its homes.
func (s *PAYGStore) SpentBytes(ctx context.Context, uid string, month time.Time) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, `
        SELECT COALESCE(sum(u.bytes_payg), 0) FROM relay_usage_monthly u
          JOIN servers s ON s.id = u.server_id
         WHERE s.owner_user_id = $1 AND u.month = $2`, uid, MonthStart(month)).Scan(&n)
	return n, err
}

// ForServer is the pay-as-you-go a home's relayed bytes may use now: its owner's settings and
// what the owner spent this month (its homes' pay-as-you-go bytes at Pro's price a GB). Off
// when Pro's price isn't set yet: nothing can be billed.
func (s *PAYGStore) ForServer(ctx context.Context, serverID string, now time.Time) (PAYG, error) {
	t := Tiers[PlanPro]
	if t.PAYGPricePerGBCents == nil || *t.PAYGPricePerGBCents <= 0 {
		return PAYG{}, nil
	}
	var uid string
	var p PAYGSettings
	err := s.DB.QueryRowContext(ctx, `
        SELECT s.owner_user_id, COALESCE(g.enabled, false), COALESCE(g.cap_cents, 0)
          FROM servers s LEFT JOIN payg_settings g ON g.user_id = s.owner_user_id
         WHERE s.id = $1`, serverID).Scan(&uid, &p.Enabled, &p.CapCents)
	if errors.Is(err, sql.ErrNoRows) {
		return PAYG{}, nil
	}
	if err != nil || !p.Enabled {
		return PAYG{}, err
	}
	spent, err := s.SpentBytes(ctx, uid, now)
	if err != nil {
		return PAYG{}, err
	}
	price := int64(*t.PAYGPricePerGBCents)
	return PAYG{On: true, CapCents: p.CapCents, PricePerGBCents: price, SpentCentsAlready: PAYGSpentCents(spent, price)}, nil
}
