package moderation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/lib/pq"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// CounterNotice is a publisher's § 512(g) counter-notice, filed from the home's Broadcast page.
type CounterNotice struct {
	ActionID    string `json:"action_id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	Explanation string `json:"explanation"`
	// Mistake: under penalty of perjury, the material was removed by mistake or misidentification.
	Mistake bool `json:"mistake"`
	// Consent: to the jurisdiction of the court for the address, and to accept service.
	Consent   bool   `json:"consent"`
	Signature string `json:"signature"`
}

// Missing is what a counter-notice lacks (the same reply shape as a notice's).
func (c CounterNotice) Missing() []string {
	var out []string
	if blank(c.ActionID) {
		out = append(out, "action_id")
	}
	if blank(c.Name) || len(c.Name) > 200 {
		out = append(out, "name")
	}
	if blank(c.Address) || len(c.Address) > 1000 {
		out = append(out, "address")
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(c.Email)); err != nil {
		out = append(out, "email")
	}
	if blank(c.Explanation) || len(c.Explanation) > maxField {
		out = append(out, "explanation")
	}
	if !c.Mistake {
		out = append(out, "mistake")
	}
	if !c.Consent {
		out = append(out, "consent")
	}
	if blank(c.Signature) || len(c.Signature) > 200 {
		out = append(out, "signature")
	}
	return out
}

// FileCounterNotice records a counter-notice for one of this home's takedowns; the claimant is
// sent a copy, and the restore window opens 10 business days later.
func (s *Store) FileCounterNotice(ctx context.Context, serverID string, c CounterNotice) (string, time.Time, error) {
	a, err := s.action(ctx, c.ActionID)
	if err != nil || a.ServerID != serverID {
		return "", time.Time{}, ErrNotFound
	}
	if a.Kind != "takedown" || a.RestoredAt != nil || a.PublisherID == "" {
		return "", time.Time{}, ErrClosed
	}
	filed := s.now()
	after := AddBusinessDays(filed, RestoreAfterDays)
	var id string
	err = s.DB.QueryRowContext(ctx, `
        INSERT INTO moderation_counter_notices (action_id, notice_id, server_id, publisher_id, name, address, phone, email,
            explanation, signature, filed_at, restore_after)
        VALUES ($1, $2, $3, $4, $5, $6, $7, lower($8), $9, $10, $11, $12)
        ON CONFLICT (action_id) DO NOTHING RETURNING id::text`,
		a.ID, nullUUID(a.NoticeID), serverID, a.PublisherID, strings.TrimSpace(c.Name), strings.TrimSpace(c.Address),
		strings.TrimSpace(c.Phone), strings.TrimSpace(c.Email), strings.TrimSpace(c.Explanation), strings.TrimSpace(c.Signature),
		filed, after).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrClosed // one counter-notice per takedown
	}
	if err != nil {
		return "", time.Time{}, err
	}
	_ = s.audit(ctx, s.DB, bc.Actor{ID: "home:" + serverID}, "counter_notice", "action", a.ID, "", map[string]any{"counter_notice": id})
	if a.NoticeID != "" {
		var to string
		if s.DB.QueryRowContext(ctx, `SELECT claimant_email FROM moderation_notices WHERE id::text = $1`, a.NoticeID).Scan(&to) == nil {
			s.send(ctx, to, "counter_notice_copy", map[string]any{"notice_id": a.NoticeID, "name": strings.TrimSpace(c.Name),
				"address": strings.TrimSpace(c.Address), "explanation": strings.TrimSpace(c.Explanation),
				"restore_after": after.Format("2006-01-02")})
		}
	}
	s.log().Info("moderation: counter-notice filed", "event", "moderation_counter_notice", "action_id", a.ID, "restore_after", after)
	return id, after, nil
}

// Lawsuit records that the claimant told the agent they went to court: the item is not restored.
func (s *Store) Lawsuit(ctx context.Context, counterID, reason string, by bc.Actor) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE moderation_counter_notices SET lawsuit_at = now(), lawsuit_by = $2
        WHERE id::text = $1 AND lawsuit_at IS NULL AND restored_at IS NULL`, counterID, by.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.audit(ctx, s.DB, by, "lawsuit", "counter_notice", counterID, reason, nil)
}

// Restore re-enables what a takedown disabled: through the same signed channel for an item or a
// source, by lifting the kill switch's entry for a channel or a publisher. A takedown on a notice
// is restored only once its counter-notice window opened (and no lawsuit) or the notice was
// withdrawn; one on a report or the admin's own knowledge whenever the admin decides. A successful
// counter-notice withdraws its strike.
func (s *Store) Restore(ctx context.Context, actionID, reason string, by bc.Actor) (Action, error) {
	a, err := s.action(ctx, actionID)
	if err != nil {
		return Action{}, err
	}
	if a.Kind != "takedown" || a.RestoredAt != nil {
		return Action{}, ErrClosed
	}
	counter := ""
	if a.Basis == BasisNotice {
		var status string
		if err := s.DB.QueryRowContext(ctx, `SELECT status FROM moderation_notices WHERE id::text = $1`, a.NoticeID).Scan(&status); err != nil {
			return Action{}, err
		}
		if status != "withdrawn" {
			var filed time.Time
			var lawsuit sql.NullTime
			err := s.DB.QueryRowContext(ctx, `SELECT id::text, filed_at, lawsuit_at FROM moderation_counter_notices WHERE action_id::text = $1`,
				a.ID).Scan(&counter, &filed, &lawsuit)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !RestoreOpen(filed, lawsuit.Valid, s.now())) {
				return Action{}, ErrRestoreClosed
			}
			if err != nil {
				return Action{}, err
			}
		}
	}
	// the kill switch's entry this takedown added comes off (a slate still holding, a channel, a
	// publisher's suspension)
	if n, ok := bc.ParseEntryID(a.Revocation); ok {
		if _, err := s.Rev.Lift(ctx, n, auditNote("restored: "+reason), by); err != nil && !errors.Is(err, bc.ErrRevocationNotFound) {
			return Action{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE moderation_actions SET restored_at = now() WHERE id::text = $1`, a.ID); err != nil {
		return Action{}, err
	}
	homeState := "none"
	if a.Scope == ScopeItem || a.Scope == ScopeSource {
		homeState = "pending"
	}
	var id string
	if err := tx.QueryRowContext(ctx, `
        INSERT INTO moderation_actions (kind, basis, scope, notice_id, report_id, restores, server_id, channel_id, publisher_id,
            at_time, item_ref, source_kind, source_ref, programme, home_state, reason, actor_id, actor_email)
        SELECT 'restore', basis, scope, notice_id, report_id, id, server_id, channel_id, publisher_id, at_time, item_ref,
            source_kind, source_ref, programme, $2, $3, $4, $5
          FROM moderation_actions WHERE id::text = $1 RETURNING id::text`, a.ID, homeState, reason, by.ID, by.Email).Scan(&id); err != nil {
		return Action{}, err
	}
	if counter != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE moderation_counter_notices SET restored_at = now() WHERE id::text = $1`, counter); err != nil {
			return Action{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE moderation_strikes SET withdrawn_at = now(), withdrawn_by = $2,
            withdraw_reason = 'successful counter-notice' WHERE action_id::text = $1 AND withdrawn_at IS NULL`, a.ID, by.ID); err != nil {
			return Action{}, err
		}
	}
	t := Target{ServerID: a.ServerID, ChannelID: a.ChannelID, PublisherID: a.PublisherID}
	params := map[string]any{"programme": a.Programme, "scope": a.Scope}
	if err := s.statement(ctx, tx, t, id, "restored", params); err != nil {
		return Action{}, err
	}
	if err := s.audit(ctx, tx, by, "restore", "action", a.ID, reason, map[string]any{"restore": id, "counter_notice": counter}); err != nil {
		return Action{}, err
	}
	if err := tx.Commit(); err != nil {
		return Action{}, err
	}
	s.push(ctx)
	s.mailPublisher(ctx, a.PublisherID, "restored", params)
	return s.action(ctx, id)
}

// RejectNotice closes a notice with no action; unfounded counts it against the notifier (Art. 23).
func (s *Store) RejectNotice(ctx context.Context, id, reason string, unfounded bool, by bc.Actor) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE moderation_notices SET status = 'rejected', decided_at = now(), decided_by = $2,
        decision_note = $3, unfounded = $4 WHERE id::text = $1 AND status = 'open'`, id, by.ID, reason, unfounded)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClosed
	}
	return s.audit(ctx, s.DB, by, "reject", "notice", id, reason, map[string]any{"unfounded": unfounded})
}

// WithdrawNotice records that the claimant withdrew an accepted notice: its strike is withdrawn,
// and its takedown may be restored at once.
func (s *Store) WithdrawNotice(ctx context.Context, id, reason string, by bc.Actor) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE moderation_notices SET status = 'withdrawn', decided_at = now(), decided_by = $2,
        decision_note = $3 WHERE id::text = $1 AND status IN ('open', 'accepted')`, id, by.ID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClosed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE moderation_strikes SET withdrawn_at = now(), withdrawn_by = $2,
        withdraw_reason = 'notice withdrawn by the claimant' WHERE notice_id::text = $1 AND withdrawn_at IS NULL`, id, by.ID); err != nil {
		return err
	}
	if err := s.audit(ctx, tx, by, "withdraw", "notice", id, reason, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// DismissReport closes a report with no action.
func (s *Store) DismissReport(ctx context.Context, id, reason string, by bc.Actor) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE moderation_reports SET status = 'dismissed', decided_at = now(), decided_by = $2,
        decision_note = $3 WHERE id::text = $1 AND status = 'open'`, id, by.ID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClosed
	}
	return s.audit(ctx, s.DB, by, "dismiss", "report", id, reason, nil)
}

// WithdrawStrike withdraws one strike (a service admin's decision, audited).
func (s *Store) WithdrawStrike(ctx context.Context, id, reason string, by bc.Actor) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE moderation_strikes SET withdrawn_at = now(), withdrawn_by = $2, withdraw_reason = $3
        WHERE id::text = $1 AND withdrawn_at IS NULL`, id, by.ID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClosed
	}
	return s.audit(ctx, s.DB, by, "withdraw", "strike", id, reason, nil)
}

// Reinstate lifts a repeat infringer's termination after an appeal (audited).
func (s *Store) Reinstate(ctx context.Context, publisherID, reason string, by bc.Actor) error {
	var id string
	var rev sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `UPDATE moderation_terminations SET reinstated_at = now(), reinstated_by = $2, reinstate_reason = $3
        WHERE publisher_id::text = $1 AND reinstated_at IS NULL RETURNING id::text, revocation_id`, publisherID, by.ID, reason).Scan(&id, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if rev.Valid {
		if _, err := s.Rev.Lift(ctx, rev.Int64, auditNote("reinstated: "+reason), by); err != nil && !errors.Is(err, bc.ErrRevocationNotFound) {
			return err
		}
	}
	if err := s.audit(ctx, s.DB, by, "reinstate", "publisher", publisherID, reason, map[string]any{"termination": id}); err != nil {
		return err
	}
	s.push(ctx)
	return nil
}

// SuspendNotifier suspends an email from the notice form (repeated manifestly unfounded notices).
func (s *Store) SuspendNotifier(ctx context.Context, email, reason string, by bc.Actor) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return bad("email is not an address")
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO moderation_notifier_blocks (email, reason, blocked_by) VALUES ($1, $2, $3)
        ON CONFLICT (email) DO UPDATE SET reason = EXCLUDED.reason, blocked_by = EXCLUDED.blocked_by, blocked_at = now()`,
		email, reason, by.ID); err != nil {
		return err
	}
	return s.audit(ctx, s.DB, by, "suspend", "notifier", email, reason, nil)
}

// NoticeRow is a notice as the queue shows it.
type NoticeRow struct {
	ID          string     `json:"id"`
	Source      string     `json:"source"`
	Status      string     `json:"status"`
	Claimant    string     `json:"claimant"`
	Email       string     `json:"email"`
	Address     string     `json:"address"`
	Phone       string     `json:"phone"`
	Work        string     `json:"work"`
	URLs        []string   `json:"urls"`
	Programme   string     `json:"programme"`
	At          *time.Time `json:"at,omitempty"`
	Explanation string     `json:"explanation"`
	Signature   string     `json:"signature"`
	Privacy     bool       `json:"privacy"`
	Urgent      bool       `json:"urgent"`
	ServerID    string     `json:"server_id,omitempty"`
	ChannelID   string     `json:"channel_id,omitempty"`
	PublisherID string     `json:"publisher_id,omitempty"`
	EdgeKnown   bool       `json:"edge_known"`
	ReceivedAt  time.Time  `json:"received_at"`
	// Unfounded is how many of this notifier's notices were rejected as manifestly unfounded.
	Unfounded int    `json:"unfounded"`
	Suspended bool   `json:"suspended"`
	Decision  string `json:"decision,omitempty"`
}

// ReportRow is a report as the queue shows it.
type ReportRow struct {
	ID          string    `json:"id"`
	Category    string    `json:"category"`
	About       string    `json:"about"`
	Status      string    `json:"status"`
	ChannelURL  string    `json:"channel"`
	ServerID    string    `json:"server_id,omitempty"`
	ChannelID   string    `json:"channel_id,omitempty"`
	PublisherID string    `json:"publisher_id,omitempty"`
	EdgeKnown   bool      `json:"edge_known"`
	Programme   string    `json:"programme"`
	At          time.Time `json:"at"`
	Email       string    `json:"email"`
	Body        string    `json:"text"`
	Urgent      bool      `json:"urgent"`
	ReceivedAt  time.Time `json:"received_at"`
}

// CounterRow is a counter-notice as the queue shows it, with its restore window.
type CounterRow struct {
	ID           string     `json:"id"`
	ActionID     string     `json:"action_id"`
	NoticeID     string     `json:"notice_id,omitempty"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	Address      string     `json:"address"`
	Explanation  string     `json:"explanation"`
	FiledAt      time.Time  `json:"filed_at"`
	RestoreAfter time.Time  `json:"restore_after"`
	Lawsuit      *time.Time `json:"lawsuit_at,omitempty"`
	Restored     *time.Time `json:"restored_at,omitempty"`
	// RestoreOpen: the queue offers restore.
	RestoreOpen bool `json:"restore_open"`
}

// StrikeRow is a strike.
type StrikeRow struct {
	ID          string     `json:"id"`
	PublisherID string     `json:"publisher_id"`
	NoticeID    string     `json:"notice_id"`
	ActionID    string     `json:"action_id"`
	At          time.Time  `json:"at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	Withdrawn   *time.Time `json:"withdrawn_at,omitempty"`
	Active      bool       `json:"active"`
}

// Queue is the console's moderation page.
type Queue struct {
	Notices      []NoticeRow  `json:"notices"`
	Reports      []ReportRow  `json:"reports"`
	Actions      []Action     `json:"actions"`
	Counters     []CounterRow `json:"counter_notices"`
	Strikes      []StrikeRow  `json:"strikes"`
	Terminated   []string     `json:"terminated"`
	Limit        int          `json:"strike_limit"`
	WindowMonths int          `json:"strike_window_months"`
}

// Queue reads the open notices and reports first, then the last 90 days of decided ones, the
// actions, the counter-notices and every strike that still counts or ended in the last year.
func (s *Store) Queue(ctx context.Context) (Queue, error) {
	q := Queue{Notices: []NoticeRow{}, Reports: []ReportRow{}, Actions: []Action{}, Counters: []CounterRow{}, Strikes: []StrikeRow{},
		Terminated: []string{}, Limit: s.policy().Limit, WindowMonths: s.policy().WindowMonths}
	rs, err := s.DB.QueryContext(ctx, `
        SELECT n.id::text, n.source, n.status, n.claimant_name, n.claimant_email, n.claimant_address, n.claimant_phone, n.work, n.urls,
               n.programme, n.at_time, n.explanation, n.signature, n.privacy, n.urgent, COALESCE(n.server_id::text, ''),
               COALESCE(n.channel_id::text, ''), COALESCE(n.publisher_id::text, ''),
               COALESCE((SELECT bc.edge_channel <> '' FROM broadcast_channels bc WHERE bc.id = n.channel_id), false),
               n.received_at,
               (SELECT count(*) FROM moderation_notices u WHERE lower(u.claimant_email) = lower(n.claimant_email) AND u.unfounded),
               EXISTS (SELECT 1 FROM moderation_notifier_blocks b WHERE b.email = lower(n.claimant_email)), n.decision_note
          FROM moderation_notices n
         WHERE n.status = 'open' OR n.received_at > now() - interval '90 days'
         ORDER BY (n.status = 'open') DESC, n.urgent DESC, n.received_at
         LIMIT 500`)
	if err != nil {
		return q, err
	}
	for rs.Next() {
		var n NoticeRow
		var at sql.NullTime
		if err := rs.Scan(&n.ID, &n.Source, &n.Status, &n.Claimant, &n.Email, &n.Address, &n.Phone, &n.Work, pq.Array(&n.URLs),
			&n.Programme, &at, &n.Explanation, &n.Signature, &n.Privacy, &n.Urgent, &n.ServerID, &n.ChannelID, &n.PublisherID,
			&n.EdgeKnown, &n.ReceivedAt, &n.Unfounded, &n.Suspended, &n.Decision); err != nil {
			_ = rs.Close()
			return q, err
		}
		n.At = timePtr(at)
		q.Notices = append(q.Notices, n)
	}
	_ = rs.Close()
	rs, err = s.DB.QueryContext(ctx, `
        SELECT r.id::text, r.category, r.about, r.status, r.channel_url, COALESCE(r.server_id::text, ''), COALESCE(r.channel_id::text, ''),
               COALESCE(r.publisher_id::text, ''),
               COALESCE((SELECT bc.edge_channel <> '' FROM broadcast_channels bc WHERE bc.id = r.channel_id), false),
               r.programme, r.at_time, r.email, r.body, r.urgent, r.received_at
          FROM moderation_reports r
         WHERE r.status = 'open' OR r.received_at > now() - interval '90 days'
         ORDER BY (r.status = 'open') DESC, r.urgent DESC, r.received_at
         LIMIT 500`)
	if err != nil {
		return q, err
	}
	for rs.Next() {
		var r ReportRow
		if err := rs.Scan(&r.ID, &r.Category, &r.About, &r.Status, &r.ChannelURL, &r.ServerID, &r.ChannelID, &r.PublisherID, &r.EdgeKnown,
			&r.Programme, &r.At, &r.Email, &r.Body, &r.Urgent, &r.ReceivedAt); err != nil {
			_ = rs.Close()
			return q, err
		}
		q.Reports = append(q.Reports, r)
	}
	_ = rs.Close()
	rs, err = s.DB.QueryContext(ctx, `SELECT `+actionCols+` FROM moderation_actions a ORDER BY a.created_at DESC LIMIT 200`)
	if err != nil {
		return q, err
	}
	for rs.Next() {
		a, err := scanAction(rs)
		if err != nil {
			_ = rs.Close()
			return q, err
		}
		q.Actions = append(q.Actions, a)
	}
	_ = rs.Close()
	if q.Counters, err = s.counters(ctx, ""); err != nil {
		return q, err
	}
	if q.Strikes, err = s.strikes(ctx, ""); err != nil {
		return q, err
	}
	rs, err = s.DB.QueryContext(ctx, `SELECT publisher_id::text FROM moderation_terminations WHERE reinstated_at IS NULL ORDER BY at`)
	if err != nil {
		return q, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var p string
		if err := rs.Scan(&p); err != nil {
			return q, err
		}
		q.Terminated = append(q.Terminated, p)
	}
	return q, rs.Err()
}

func (s *Store) counters(ctx context.Context, serverID string) ([]CounterRow, error) {
	rs, err := s.DB.QueryContext(ctx, `SELECT id::text, action_id::text, COALESCE(notice_id::text, ''), name, email, address, explanation,
        filed_at, restore_after, lawsuit_at, restored_at
        FROM moderation_counter_notices WHERE ($1 = '' OR server_id::text = $1) ORDER BY filed_at DESC LIMIT 200`, serverID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []CounterRow{}
	for rs.Next() {
		var c CounterRow
		var lawsuit, restored sql.NullTime
		if err := rs.Scan(&c.ID, &c.ActionID, &c.NoticeID, &c.Name, &c.Email, &c.Address, &c.Explanation, &c.FiledAt, &c.RestoreAfter,
			&lawsuit, &restored); err != nil {
			return nil, err
		}
		c.Lawsuit, c.Restored = timePtr(lawsuit), timePtr(restored)
		c.RestoreOpen = c.Restored == nil && RestoreOpen(c.FiledAt, c.Lawsuit != nil, s.now())
		out = append(out, c)
	}
	return out, rs.Err()
}

func (s *Store) strikes(ctx context.Context, publisher string) ([]StrikeRow, error) {
	rs, err := s.DB.QueryContext(ctx, `SELECT id::text, publisher_id::text, notice_id::text, action_id::text, at, expires_at, withdrawn_at
        FROM moderation_strikes WHERE ($1 = '' OR publisher_id::text = $1) AND expires_at > now() - interval '1 year'
        ORDER BY at DESC LIMIT 500`, publisher)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []StrikeRow{}
	now := s.now()
	for rs.Next() {
		var st StrikeRow
		var w sql.NullTime
		if err := rs.Scan(&st.ID, &st.PublisherID, &st.NoticeID, &st.ActionID, &st.At, &st.ExpiresAt, &w); err != nil {
			return nil, err
		}
		st.Withdrawn = timePtr(w)
		st.Active = st.Withdrawn == nil && now.Before(st.ExpiresAt)
		out = append(out, st)
	}
	return out, rs.Err()
}

// Statement is one thing the publisher is told; the Broadcast page renders it from its kind and
// params (broadcast.takedown.*, in English and Arabic).
type Statement struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	ActionID  string          `json:"action_id,omitempty"`
	Params    json.RawMessage `json:"params"`
	CreatedAt time.Time       `json:"created_at"`
	// Counter is the counter-notice filed against this statement's takedown, if any.
	Counter *CounterRow `json:"counter_notice,omitempty"`
	// CanCounter: a takedown on a notice, not restored, with no counter-notice yet.
	CanCounter bool `json:"can_counter"`
}

// HomeReport is a report about the home's content, without the reporter's email.
type HomeReport struct {
	ID         string    `json:"id"`
	Category   string    `json:"category"`
	ChannelURL string    `json:"channel"`
	Programme  string    `json:"programme"`
	At         time.Time `json:"at"`
	Body       string    `json:"text"`
	Status     string    `json:"status"`
	ReceivedAt time.Time `json:"received_at"`
}

// HomeView is what a home's Broadcast page shows: its statements, the reports about its content,
// and its publisher's standing. A home sees only its own records.
type HomeView struct {
	Statements   []Statement  `json:"statements"`
	Reports      []HomeReport `json:"reports"`
	Strikes      int          `json:"strikes"`
	Limit        int          `json:"strike_limit"`
	WindowMonths int          `json:"strike_window_months"`
	Terminated   bool         `json:"terminated"`
}

// Home is a home's view.
func (s *Store) Home(ctx context.Context, serverID string) (HomeView, error) {
	v := HomeView{Statements: []Statement{}, Reports: []HomeReport{}, Limit: s.policy().Limit, WindowMonths: s.policy().WindowMonths}
	var owner string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(owner_user_id::text, '') FROM servers WHERE id::text = $1`, serverID).Scan(&owner); err != nil {
		return v, err
	}
	counters, err := s.counters(ctx, serverID)
	if err != nil {
		return v, err
	}
	byAction := map[string]*CounterRow{}
	for i := range counters {
		byAction[counters[i].ActionID] = &counters[i]
	}
	rs, err := s.DB.QueryContext(ctx, `SELECT st.id::text, st.kind, COALESCE(st.action_id::text, ''), st.params, st.created_at,
            COALESCE(a.basis = 'notice' AND a.kind = 'takedown' AND a.restored_at IS NULL, false)
        FROM moderation_statements st LEFT JOIN moderation_actions a ON a.id = st.action_id
        WHERE st.server_id::text = $1 ORDER BY st.created_at DESC LIMIT 100`, serverID)
	if err != nil {
		return v, err
	}
	for rs.Next() {
		var st Statement
		var raw []byte
		var open bool
		if err := rs.Scan(&st.ID, &st.Kind, &st.ActionID, &raw, &st.CreatedAt, &open); err != nil {
			_ = rs.Close()
			return v, err
		}
		st.Params = raw
		if st.Kind == "takedown" {
			st.Counter = byAction[st.ActionID]
			st.CanCounter = open && st.Counter == nil
		}
		v.Statements = append(v.Statements, st)
	}
	_ = rs.Close()
	rs, err = s.DB.QueryContext(ctx, `SELECT id::text, category, channel_url, programme, at_time, body, status, received_at
        FROM moderation_reports WHERE server_id::text = $1 AND about = 'content' ORDER BY received_at DESC LIMIT 100`, serverID)
	if err != nil {
		return v, err
	}
	for rs.Next() {
		var r HomeReport
		if err := rs.Scan(&r.ID, &r.Category, &r.ChannelURL, &r.Programme, &r.At, &r.Body, &r.Status, &r.ReceivedAt); err != nil {
			_ = rs.Close()
			return v, err
		}
		v.Reports = append(v.Reports, r)
	}
	_ = rs.Close()
	if owner != "" {
		if v.Strikes, err = s.activeStrikes(ctx, s.DB, owner); err != nil {
			return v, err
		}
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM moderation_terminations WHERE publisher_id::text = $1
            AND reinstated_at IS NULL`, owner).Scan(&n); err != nil {
			return v, err
		}
		v.Terminated = n > 0
	}
	return v, nil
}

// RecordsRetentionYears is broadcast.records_retention_years (to confirm with counsel).
const RecordsRetentionYears = 3

// Sweep deletes closed records older than the retention: notices, reports and everything that
// hangs off them, once decided and past the retention. Strikes past it no longer count anyway.
func (s *Store) Sweep(ctx context.Context, years int) (int64, error) {
	if years <= 0 {
		years = RecordsRetentionYears
	}
	cut := s.now().AddDate(-years, 0, 0)
	var total int64
	for _, q := range []string{
		`DELETE FROM moderation_statements WHERE created_at < $1`,
		`DELETE FROM moderation_counter_notices WHERE filed_at < $1`,
		`DELETE FROM moderation_strikes WHERE expires_at < $1`,
		`DELETE FROM moderation_actions WHERE created_at < $1
            AND NOT EXISTS (SELECT 1 FROM moderation_strikes st WHERE st.action_id = moderation_actions.id)
            AND NOT EXISTS (SELECT 1 FROM moderation_counter_notices c WHERE c.action_id = moderation_actions.id)
            AND NOT EXISTS (SELECT 1 FROM moderation_actions r WHERE r.restores = moderation_actions.id AND r.created_at >= $1)`,
		`DELETE FROM moderation_notices WHERE COALESCE(decided_at, received_at) < $1 AND status <> 'open'
            AND NOT EXISTS (SELECT 1 FROM moderation_actions a WHERE a.notice_id = moderation_notices.id)`,
		`DELETE FROM moderation_reports WHERE COALESCE(decided_at, received_at) < $1 AND status <> 'open'
            AND NOT EXISTS (SELECT 1 FROM moderation_actions a WHERE a.report_id = moderation_reports.id)`,
		`DELETE FROM moderation_audit WHERE at < $1`,
	} {
		res, err := s.DB.ExecContext(ctx, q, cut)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// Run sweeps once a day until ctx ends (the worker's leader runs it).
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := s.Sweep(ctx, RecordsRetentionYears); err != nil {
			s.log().Warn("moderation: sweeping old records", "err", err)
		} else if n > 0 {
			s.log().Info("moderation: old records swept", "rows", n, "event", "moderation_sweep")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Audit records a staff action that changes nothing else (an export of the records).
func (s *Store) Audit(ctx context.Context, by bc.Actor, verb, kind, id, reason string, detail map[string]any) error {
	return s.audit(ctx, s.DB, by, verb, kind, id, reason, detail)
}
