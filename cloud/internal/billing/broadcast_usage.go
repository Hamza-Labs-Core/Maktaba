package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"
)

// Story 50.20 §3: viewer-hour metering. The broadcast edge (50.21's Worker, its PublisherControl
// Durable Object) counts viewer-minutes per publisher and posts them every 5 minutes as windows
// (POST /_edge/usage); BroadcastStore takes each window once, places its minutes (allowance,
// pay-as-you-go, lowest) and answers with the publisher's state, which the edge holds to decide
// new sessions' modes. BroadcastReporter bills pay-as-you-go hourly, sends the notices and
// reconciles with the provider daily.

// WindowLength is the edge's posting window.
const WindowLength = 5 * time.Minute

// WindowRetention is how long taken windows are kept to refuse a repeat (the edge keeps 7 days
// of its own; 35 days covers any re-post).
const WindowRetention = 35 * 24 * time.Hour

// BroadcastAgreementVersion is the Broadcaster agreement's current version (§0): raising it asks
// every account to accept again at its next publish. A var only so tests can raise it.
var BroadcastAgreementVersion = 1

// Errors of the usage intake.
var (
	ErrUnknownPublisher = errors.New("billing: unknown publisher")
	ErrBadWindow        = errors.New("billing: a window must start on a 5-minute boundary, with minutes from 0 to 100000")
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID says whether s is a UUID (publisher ids are home ids).
func IsUUID(s string) bool { return uuidRE.MatchString(s) }

// UsageWindow is one publisher's 5-minute window as the edge posts it. Its idempotency key is
// <publisher>:<window> (WindowKey).
type UsageWindow struct {
	Publisher     string    `json:"publisher"`
	Window        time.Time `json:"window"`
	MinutesFull   int64     `json:"minutes_full"`
	MinutesLowest int64     `json:"minutes_lowest"`
}

// WindowKey is a window's idempotency key.
func WindowKey(publisher string, window time.Time) string {
	return publisher + ":" + window.UTC().Format(time.RFC3339)
}

// Valid checks a window's shape.
func (w UsageWindow) Valid() error {
	if !IsUUID(w.Publisher) {
		return ErrUnknownPublisher
	}
	if w.Window.IsZero() || !w.Window.UTC().Truncate(WindowLength).Equal(w.Window.UTC()) ||
		w.MinutesFull < 0 || w.MinutesLowest < 0 || w.MinutesFull > 100_000 || w.MinutesLowest > 100_000 {
		return ErrBadWindow
	}
	return nil
}

// BroadcastStore is the publishers' plan state and usage.
type BroadcastStore struct {
	DB *sql.DB
	// Enabled is broadcast.enabled, the broadcasting gate: off, only test publishers a service
	// admin granted may broadcast.
	Enabled bool
	// Enforce is broadcast.plans.enforce (rollback: off, new sessions get full up to the
	// concurrent-viewer limit).
	Enforce bool
	Now     func() time.Time
}

func (s *BroadcastStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Publisher is a home as a publisher.
type Publisher struct {
	ServerID        string
	OwnerID         string
	Plan            string // servers.plan (the owner's effective plan)
	BroadcastPlan   string // servers.broadcast_plan ("" or broadcaster)
	OwnerActive     bool
	TestPublisher   bool
	ExtraChannels   int
	ExtraViewers100 int
}

// Tier is what the publisher may publish under (nil: nothing).
func (p Publisher) Tier() *BroadcastTier {
	if !p.OwnerActive {
		return nil
	}
	return BroadcastFor(p.Plan, p.BroadcastPlan)
}

// Limits are its tier's limits with the extra units bought.
func (p Publisher) Limits() Limits {
	t := p.Tier()
	if t == nil {
		return Limits{}
	}
	return t.LimitsWith(p.ExtraChannels, p.ExtraViewers100)
}

// Gated says whether the broadcasting gate lets this publisher broadcast.
func (s *BroadcastStore) Gated(p Publisher) bool { return s.Enabled || p.TestPublisher }

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Publisher loads a home as a publisher.
func (s *BroadcastStore) Publisher(ctx context.Context, serverID string) (Publisher, error) {
	return loadPublisher(ctx, s.DB, serverID)
}

func loadPublisher(ctx context.Context, q queryer, serverID string) (Publisher, error) {
	if !IsUUID(serverID) {
		return Publisher{}, ErrUnknownPublisher
	}
	var p Publisher
	var bp sql.NullString
	var status string
	err := q.QueryRowContext(ctx, `
        SELECT s.id::text, s.owner_user_id::text, s.plan, s.broadcast_plan, u.status, s.broadcast_test_publisher,
               s.broadcast_extra_channels, s.broadcast_extra_viewers
          FROM servers s JOIN users u ON u.id = s.owner_user_id
         WHERE s.id = $1`, serverID).Scan(&p.ServerID, &p.OwnerID, &p.Plan, &bp, &status, &p.TestPublisher,
		&p.ExtraChannels, &p.ExtraViewers100)
	if errors.Is(err, sql.ErrNoRows) {
		return Publisher{}, ErrUnknownPublisher
	}
	if err != nil {
		return Publisher{}, err
	}
	p.BroadcastPlan = bp.String
	p.OwnerActive = status == "active"
	return p, nil
}

// BroadcastPAYGSettings are a publisher's pay-as-you-go viewer-hours settings.
type BroadcastPAYGSettings struct {
	Enabled  bool  `json:"enabled"`
	CapCents int64 `json:"cap_cents"`
}

// PAYGSettings are a publisher's (off when it has none).
func (s *BroadcastStore) PAYGSettings(ctx context.Context, serverID string) (BroadcastPAYGSettings, error) {
	return paygSettings(ctx, s.DB, serverID)
}

func paygSettings(ctx context.Context, q queryer, serverID string) (BroadcastPAYGSettings, error) {
	var p BroadcastPAYGSettings
	err := q.QueryRowContext(ctx, `SELECT enabled, cap_cents FROM broadcast_payg_settings WHERE server_id = $1`, serverID).Scan(&p.Enabled, &p.CapCents)
	if errors.Is(err, sql.ErrNoRows) {
		return BroadcastPAYGSettings{}, nil
	}
	return p, err
}

// SetPAYGSettings keeps them: on needs a cap above zero. A cap lowered below what is spent stops
// pay-as-you-go at once (what was spent stays billed).
func (s *BroadcastStore) SetPAYGSettings(ctx context.Context, serverID string, p BroadcastPAYGSettings) error {
	if p.Enabled && p.CapCents <= 0 {
		return ErrPAYGCapRequired
	}
	p.CapCents = max(p.CapCents, 0)
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO broadcast_payg_settings (server_id, enabled, cap_cents, updated_at) VALUES ($1, $2, $3, now())
        ON CONFLICT (server_id) DO UPDATE SET enabled = EXCLUDED.enabled, cap_cents = EXCLUDED.cap_cents, updated_at = now()`,
		serverID, p.Enabled, p.CapCents)
	return err
}

// BroadcastPAYGPrice is the add-on's price of 100 viewer-hours in cents (0: TBD, nothing can be
// billed and pay-as-you-go stays off).
func BroadcastPAYGPrice() int64 {
	if Broadcaster.PAYGPricePer100HCents == nil {
		return 0
	}
	return int64(*Broadcaster.PAYGPricePer100HCents)
}

// paygFor is the pay-as-you-go the publisher's next minutes may use.
func paygFor(tier *BroadcastTier, set BroadcastPAYGSettings, u BroadcastUsage) BroadcastPAYG {
	price := BroadcastPAYGPrice()
	p := BroadcastPAYG{CapCents: set.CapCents, SpentCents: BroadcastPAYGCents(u.MinutesPAYG, price),
		Minutes: u.MinutesPAYG, CapMinutes: BroadcastCapMinutes(set.CapCents, price)}
	p.On = tier != nil && tier.PAYG && price > 0 && set.Enabled
	return p
}

// monthExpr is the usage month: the API database's clock at insert (§ edge cases, clock skew).
const monthExpr = `date_trunc('month', now() AT TIME ZONE 'UTC')::date`

// RecordWindow takes one window (story 50.20 §3): a window already taken (the same
// <publisher>:<window>) is counted once and reports dup. Its full-quality minutes go to the
// allowance, then pay-as-you-go up to the cap's worth, then full again (never billed); its
// lowest minutes to lowest. The month is the database's clock now.
func (s *BroadcastStore) RecordWindow(ctx context.Context, w UsageWindow) (dup bool, err error) {
	if err := w.Valid(); err != nil {
		return false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := loadPublisher(ctx, tx, w.Publisher)
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `
        INSERT INTO broadcast_usage_windows (publisher_id, window_start, minutes_full, minutes_lowest)
        VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, w.Publisher, w.Window.UTC(), w.MinutesFull, w.MinutesLowest)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return true, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO broadcast_usage_monthly (publisher_id, month) VALUES ($1, `+monthExpr+`) ON CONFLICT DO NOTHING`,
		w.Publisher); err != nil {
		return false, err
	}
	var u BroadcastUsage
	if err := tx.QueryRowContext(ctx, `
        SELECT minutes_full, minutes_lowest, minutes_payg FROM broadcast_usage_monthly
         WHERE publisher_id = $1 AND month = `+monthExpr+` FOR UPDATE`, w.Publisher).Scan(&u.MinutesFull, &u.MinutesLowest, &u.MinutesPAYG); err != nil {
		return false, err
	}
	set, err := paygSettings(ctx, tx, w.Publisher)
	if err != nil {
		return false, err
	}
	tier := p.Tier()
	toFull, toPAYG := BroadcastSplit(tier, u, w.MinutesFull, paygFor(tier, set, u), BroadcastPAYGPrice())
	if _, err := tx.ExecContext(ctx, `
        UPDATE broadcast_usage_monthly SET minutes_full = minutes_full + $2, minutes_payg = minutes_payg + $3,
               minutes_lowest = minutes_lowest + $4, updated_at = now()
         WHERE publisher_id = $1 AND month = `+monthExpr, w.Publisher, toFull, toPAYG, w.MinutesLowest); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// Usage is a publisher's viewer-minutes in the database's current month.
func (s *BroadcastStore) Usage(ctx context.Context, serverID string) (BroadcastUsage, time.Time, error) {
	var u BroadcastUsage
	var month time.Time
	err := s.DB.QueryRowContext(ctx, `
        SELECT m.month, COALESCE(u.minutes_full, 0), COALESCE(u.minutes_lowest, 0), COALESCE(u.minutes_payg, 0)
          FROM (SELECT `+monthExpr+` AS month) m
          LEFT JOIN broadcast_usage_monthly u ON u.publisher_id = $1 AND u.month = m.month`, serverID).
		Scan(&month, &u.MinutesFull, &u.MinutesLowest, &u.MinutesPAYG)
	return u, month, err
}

// PublisherState is what the edge holds per publisher in PublisherControl (story 50.20 §2):
// the cloud returns it with every usage post, serves it at GET /_edge/publishers/{id}, and
// pushes the same shape on a change once 50.26's push lands. Mode is for NEW sessions; the
// edge also answers wait at MaxConcurrentViewers.
type PublisherState struct {
	Publisher string `json:"publisher"`
	// Plan is "" (none), guest or broadcaster.
	Plan    string `json:"plan"`
	Mode    string `json:"mode"`
	Enforce bool   `json:"enforce"`
	Limits
	MaxRungs         int            `json:"max_rungs"`
	MaxTopKbps       int            `json:"max_top_kbps"`
	Access           []string       `json:"access"`
	OnDemandOnly     bool           `json:"on_demand_only"`
	Month            string         `json:"month"`
	Usage            BroadcastUsage `json:"usage"`
	AllowanceMinutes int64          `json:"allowance_minutes"`
	// AgreementAccepted: the home's owner accepted the current Broadcaster agreement (the
	// home's publish check 2, story 50.22).
	AgreementAccepted bool `json:"agreement_accepted"`
	// AgreementVersion is the current agreement's version (stored with the home's rights
	// confirmation).
	AgreementVersion int `json:"agreement_version"`
	FairUseMinutes   int64          `json:"fair_use_minutes"`
	PAYG             BroadcastPAYG  `json:"payg"`
	At               time.Time      `json:"at"`
}

// State is a publisher's state now.
func (s *BroadcastStore) State(ctx context.Context, serverID string) (PublisherState, error) {
	p, err := s.Publisher(ctx, serverID)
	if err != nil {
		return PublisherState{}, err
	}
	u, month, err := s.Usage(ctx, serverID)
	if err != nil {
		return PublisherState{}, err
	}
	set, err := s.PAYGSettings(ctx, serverID)
	if err != nil {
		return PublisherState{}, err
	}
	st := s.stateOf(p, u, month, set)
	if p.OwnerID != "" {
		if st.AgreementAccepted, err = s.AgreementAccepted(ctx, p.OwnerID); err != nil {
			return PublisherState{}, err
		}
	}
	return st, nil
}

func (s *BroadcastStore) stateOf(p Publisher, u BroadcastUsage, month time.Time, set BroadcastPAYGSettings) PublisherState {
	st := PublisherState{Publisher: p.ServerID, Enforce: s.Enforce, Month: month.Format("2006-01"), Usage: u,
		Access: []string{}, At: s.now(), AgreementVersion: BroadcastAgreementVersion}
	tier := p.Tier()
	payg := paygFor(tier, set, u)
	st.PAYG = payg
	st.Mode = BroadcastModeFor(BroadcastInput{Tier: tier, Off: !s.Gated(p), Usage: u, PAYG: payg,
		MaxConcurrent: p.Limits().MaxConcurrentViewers, Enforce: s.Enforce})
	if tier == nil {
		return st
	}
	st.Plan = tier.ID
	st.Limits = p.Limits()
	st.MaxRungs, st.MaxTopKbps = tier.MaxRungs, tier.MaxTopKbps
	st.Access = append(st.Access, tier.Access...)
	st.OnDemandOnly = tier.OnDemandOnly
	st.AllowanceMinutes, st.FairUseMinutes = tier.AllowanceMinutes(), tier.FairUseMinutes()
	return st
}

// SweepWindows deletes taken windows past WindowRetention.
func (s *BroadcastStore) SweepWindows(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM broadcast_usage_windows WHERE window_start < $1`, s.now().Add(-WindowRetention))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Notice kinds (§5): 80 % and 100 % of the allowance and of the pay-as-you-go cap, once a month.
const (
	NoticeAllowanceNear = "allowance_80"
	NoticeAllowanceUsed = "allowance_100"
	NoticeCapNear       = "cap_80"
	NoticeCapReached    = "cap_100"
)

// BroadcastNotices are the notices a usage state has reached, in order.
func BroadcastNotices(st PublisherState) []string {
	var out []string
	if st.AllowanceMinutes > 0 {
		used := st.Usage.MinutesFull
		if used*100 >= st.AllowanceMinutes*80 {
			out = append(out, NoticeAllowanceNear)
		}
		if used >= st.AllowanceMinutes {
			out = append(out, NoticeAllowanceUsed)
		}
	}
	if st.PAYG.On && st.PAYG.CapCents > 0 {
		if st.PAYG.SpentCents*100 >= st.PAYG.CapCents*80 {
			out = append(out, NoticeCapNear)
		}
		if st.PAYG.SpentCents >= st.PAYG.CapCents {
			out = append(out, NoticeCapReached)
		}
	}
	return out
}

// BroadcastReporter sends the pay-as-you-go viewer-hours to the payment provider (story 50.20
// §4): every hour it queues each Broadcaster publisher's whole hours not queued yet, clamped so
// a publisher is never billed past its cap, then sends what is queued with each report's ID as
// the idempotency key. A provider that is down leaves them queued; they go later under the same
// IDs. It also sends the 80 %/100 % notices, reconciles daily and sweeps old windows.
type BroadcastReporter struct {
	Store    *BroadcastStore
	Provider Provider
	Logger   *slog.Logger
	// Enabled is billing.broadcast_payg.enabled: off, no new reports are queued (queued ones
	// still go, so nothing used goes unbilled).
	Enabled bool
	// Notify tells a publisher's owner about a notice kind (email; the Billing page shows it
	// in-app from the state).
	Notify func(ctx context.Context, serverID, kind string) error
	Now    func() time.Time
}

func (r *BroadcastReporter) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *BroadcastReporter) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// BroadcastReportID is a report's idempotency key: <publisher>:<month>:<hour>.
func BroadcastReportID(serverID string, month, hour time.Time) string {
	return fmt.Sprintf("%s:%s:%s", serverID, MonthStart(month).Format("2006-01"), hour.UTC().Format("2006-01-02T15"))
}

// Enqueue queues this hour's reports for month: per Broadcaster publisher with pay-as-you-go on
// and a live Pro subscription, the whole viewer-hours not queued yet, within the cap's worth.
func (r *BroadcastReporter) Enqueue(ctx context.Context, month time.Time) (int, error) {
	price := BroadcastPAYGPrice()
	if !r.Enabled || price <= 0 {
		return 0, nil
	}
	db := r.Store.DB
	rows, err := db.QueryContext(ctx, `
        SELECT s.id::text, s.owner_user_id::text, g.cap_cents, u.minutes_payg,
               COALESCE((SELECT sum(o.hours) FROM broadcast_usage_outbox o WHERE o.server_id = s.id AND o.month = u.month), 0)
          FROM broadcast_usage_monthly u
          JOIN servers s ON s.id = u.publisher_id
          JOIN broadcast_payg_settings g ON g.server_id = s.id
          JOIN subscriptions sub ON sub.user_id = s.owner_user_id
         WHERE u.month = $1 AND u.minutes_payg >= 60 AND g.enabled AND s.broadcast_plan = $2
           AND sub.plan = $3 AND sub.status IN ('active','trialing','past_due')
         ORDER BY s.id`, MonthStart(month), BroadcastPlanBroadcaster, PlanPro)
	if err != nil {
		return 0, err
	}
	type due struct {
		server, user string
		hours        int64
	}
	var dues []due
	for rows.Next() {
		var server, user string
		var capCents, minutes, queued int64
		if err := rows.Scan(&server, &user, &capCents, &minutes, &queued); err != nil {
			_ = rows.Close()
			return 0, err
		}
		allowed := capCents * 100 / price // the cap's worth in whole hours (an hour is price/100)
		if h := min(minutes/60, allowed) - queued; h > 0 {
			dues = append(dues, due{server, user, h})
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	hour := r.now().Truncate(time.Hour)
	queued := 0
	for _, d := range dues {
		res, err := db.ExecContext(ctx, `
            INSERT INTO broadcast_usage_outbox (id, server_id, user_id, month, hours) VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (id) DO NOTHING`, BroadcastReportID(d.server, month, hour), d.server, d.user, MonthStart(month), d.hours)
		if err != nil {
			return queued, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			queued++
		}
	}
	return queued, nil
}

// Send sends the queued reports, oldest first. A failure stays queued for the next run.
func (r *BroadcastReporter) Send(ctx context.Context) (sent int, err error) {
	db := r.Store.DB
	rows, err := db.QueryContext(ctx, `
        SELECT o.id, o.server_id::text, o.hours, o.month, o.created_at,
               COALESCE(s.provider_subscription_id, ''), COALESCE(s.provider_customer_id, '')
          FROM broadcast_usage_outbox o LEFT JOIN subscriptions s ON s.user_id = o.user_id
         WHERE o.sent_at IS NULL ORDER BY o.created_at LIMIT 500`)
	if err != nil {
		return 0, err
	}
	var reports []BroadcastUsageReport
	for rows.Next() {
		var u BroadcastUsageReport
		var month, created time.Time
		if err := rows.Scan(&u.ID, &u.ServerID, &u.Hours, &month, &created, &u.SubscriptionID, &u.CustomerID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		// a report sent on the 1st for the month before is dated inside that month
		u.At = created.UTC()
		if end := MonthStart(month).AddDate(0, 1, 0).Add(-time.Second); u.At.After(end) {
			u.At = end
		}
		reports = append(reports, u)
	}
	_ = rows.Close()
	for _, u := range reports {
		if perr := r.Provider.ReportBroadcastUsage(ctx, u); perr != nil {
			_, _ = db.ExecContext(ctx, `UPDATE broadcast_usage_outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`, u.ID, perr.Error())
			err = perr
			continue
		}
		if _, uerr := db.ExecContext(ctx, `UPDATE broadcast_usage_outbox SET sent_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1`, u.ID); uerr != nil {
			return sent, uerr
		}
		sent++
	}
	return sent, err
}

// Notices sends each publisher's owner the notices its state reached this month, once each.
func (r *BroadcastReporter) Notices(ctx context.Context) error {
	if r.Notify == nil {
		return nil
	}
	db := r.Store.DB
	rows, err := db.QueryContext(ctx, `SELECT publisher_id::text FROM broadcast_usage_monthly WHERE month = `+monthExpr)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	for _, id := range ids {
		st, err := r.Store.State(ctx, id)
		if errors.Is(err, ErrUnknownPublisher) {
			continue
		}
		if err != nil {
			return err
		}
		month, _ := time.Parse("2006-01", st.Month)
		for _, kind := range BroadcastNotices(st) {
			var one int
			err := db.QueryRowContext(ctx, `SELECT 1 FROM broadcast_notices WHERE server_id = $1 AND month = $2 AND kind = $3`, id, month, kind).Scan(&one)
			if err == nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := r.Notify(ctx, id, kind); err != nil {
				r.log().Warn("billing: broadcast notice", "server_id", id, "kind", kind, "err", err)
				continue
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO broadcast_notices (server_id, month, kind) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
				id, month, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

// Reconcile compares, per publisher, the viewer-hours sent this month with what the provider
// recorded; a difference over 60 viewer-minutes is logged as broadcast_usage_mismatch (SEV3).
func (r *BroadcastReporter) Reconcile(ctx context.Context) (mismatches int, err error) {
	month := MonthStart(r.now())
	rows, err := r.Store.DB.QueryContext(ctx, `
        SELECT o.server_id::text, COALESCE(s.provider_subscription_id, ''), COALESCE(sum(o.hours), 0)
          FROM broadcast_usage_outbox o JOIN subscriptions s ON s.user_id = o.user_id
         WHERE o.month = $1 AND o.sent_at IS NOT NULL
         GROUP BY o.server_id, s.provider_subscription_id`, month)
	if err != nil {
		return 0, err
	}
	type pub struct {
		server, sub string
		hours       int64
	}
	var pubs []pub
	for rows.Next() {
		var p pub
		if err := rows.Scan(&p.server, &p.sub, &p.hours); err != nil {
			_ = rows.Close()
			return 0, err
		}
		pubs = append(pubs, p)
	}
	_ = rows.Close()
	for _, p := range pubs {
		recorded, perr := r.Provider.ReportedBroadcastUsage(ctx, p.sub, p.server, month)
		if perr != nil {
			err = perr
			continue
		}
		if d := (recorded - p.hours) * 60; d > 60 || d < -60 {
			mismatches++
			r.log().Warn("billing: the provider's viewer-hours differ from ours", "server_id", p.server, "subscription", p.sub,
				"ours_hours", p.hours, "provider_hours", recorded, "event", "broadcast_usage_mismatch", "severity", "SEV3")
		}
	}
	return mismatches, err
}

// Run reports every interval (this month's, and on the 1st the month before too, so its last
// hours go out before the provider finalises the invoice), sends the notices, reconciles and
// sweeps once a day.
func (r *BroadcastReporter) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	var lastDaily time.Time
	for {
		now := r.now()
		months := []time.Time{now}
		if now.Day() == 1 {
			months = append(months, MonthStart(now).AddDate(0, -1, 0))
		}
		for _, m := range months {
			if _, err := r.Enqueue(ctx, m); err != nil {
				r.log().Warn("billing: queueing viewer-hour reports", "err", err)
			}
		}
		if n, err := r.Send(ctx); err != nil {
			r.log().Warn("billing: viewer-hour reports stay queued", "sent", n, "err", err, "event", "broadcast_usage_queued")
		}
		if err := r.Notices(ctx); err != nil {
			r.log().Warn("billing: broadcast notices", "err", err)
		}
		if now.Sub(lastDaily) >= 24*time.Hour {
			if _, err := r.Reconcile(ctx); err == nil {
				lastDaily = now
			}
			if _, err := r.Store.SweepWindows(ctx); err != nil {
				r.log().Warn("billing: sweeping usage windows", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// AgreementAccepted says whether an account accepted the current Broadcaster agreement (§0).
func (s *BroadcastStore) AgreementAccepted(ctx context.Context, uid string) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM broadcast_agreements WHERE account_id = $1 AND version = $2`,
		uid, BroadcastAgreementVersion).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Downgrade is a home leaving Broadcaster for guest sharing (story 50.20, edge cases): the
// channels past guest sharing's one are unpublished (the oldest stays), and the one left
// becomes invite-only and on demand, so its old open links ask for an invite.
func Downgrade(ctx context.Context, tx *sql.Tx, serverID string) error {
	if _, err := tx.ExecContext(ctx, `
        UPDATE broadcast_channels SET unpublished_at = now()
         WHERE server_id = $1 AND unpublished_at IS NULL
           AND id <> (SELECT id FROM broadcast_channels WHERE server_id = $1 AND unpublished_at IS NULL
                       ORDER BY published_at, id LIMIT 1)`, serverID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
        UPDATE broadcast_channels SET access = $2, on_demand = true
         WHERE server_id = $1 AND unpublished_at IS NULL`, serverID, AccessInvite)
	return err
}
