package moderation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
)

// Store is the moderation queue and everything it decides (migration 00500025): notices, reports,
// actions and their home instructions, strikes, counter-notices, statements, terminations, the
// suspended notifiers and the audit of every staff action.
type Store struct {
	DB *sql.DB
	// Rev is the kill switch's entries (story 50.25): a programme's slate, a channel, a publisher.
	Rev *bc.RevocationStore
	// Push sends the new revocation list to the edge (nil: the edge's minute pull carries it).
	Push   func(ctx context.Context)
	Mailer mail.Mailer
	Policy Policy
	Logger *slog.Logger
	Now    func() time.Time
}

// Errors the handlers answer with.
var (
	ErrNotFound      = errors.New("moderation: not found")
	ErrClosed        = errors.New("moderation: already decided")
	ErrNoTarget      = errors.New("moderation: the action names no channel or publisher")
	ErrEdgeUnknown   = errors.New("moderation: the channel's edge id is not known yet")
	ErrRestoreClosed = errors.New("moderation: the restore window is not open")
	ErrBadRequest    = errors.New("moderation: bad request")
)

// System is the actor of what the cloud does by itself (a slate shortened when the home confirms).
var System = bc.Actor{ID: "system:moderation"}

// SlateMax is how long a programme's slate holds when the item's end is not known: until the home
// confirms the takedown (and says when the item's slot ends) or this passes.
const SlateMax = 4 * time.Hour

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) policy() Policy {
	if s.Policy.Limit > 0 {
		return s.Policy
	}
	return DefaultPolicy
}

func (s *Store) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, a...))
}

// Target is a published channel and who publishes it.
type Target struct {
	ServerID    string
	ChannelID   string
	PublisherID string
	EdgeChannel string
}

// Resolve finds the channel a watch URL names (the live one, else the newest unpublished).
func (s *Store) Resolve(ctx context.Context, ref ChannelRef) (Target, bool, error) {
	var t Target
	err := s.DB.QueryRowContext(ctx, `
        SELECT bc.id::text, bc.server_id::text, COALESCE(sv.owner_user_id::text, ''), COALESCE(bc.edge_channel, '')
          FROM broadcast_channels bc JOIN servers sv ON sv.id = bc.server_id
         WHERE sv.slug = $1 AND bc.slug = $2
         ORDER BY (bc.unpublished_at IS NULL) DESC, bc.published_at DESC
         LIMIT 1`, ref.Publisher, ref.Channel).Scan(&t.ChannelID, &t.ServerID, &t.PublisherID, &t.EdgeChannel)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, false, nil
	}
	return t, err == nil, err
}

// channel is a channel's target by its cloud id.
func (s *Store) channel(ctx context.Context, id string) (Target, error) {
	var t Target
	err := s.DB.QueryRowContext(ctx, `
        SELECT bc.id::text, bc.server_id::text, COALESCE(sv.owner_user_id::text, ''), COALESCE(bc.edge_channel, '')
          FROM broadcast_channels bc JOIN servers sv ON sv.id = bc.server_id
         WHERE bc.id::text = $1`, id).Scan(&t.ChannelID, &t.ServerID, &t.PublisherID, &t.EdgeChannel)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	return t, err
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NotifierBlocked reports whether an email was suspended from the form (DSA Art. 23).
func (s *Store) NotifierBlocked(ctx context.Context, email string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM moderation_notifier_blocks WHERE email = lower($1)`,
		strings.TrimSpace(email)).Scan(&n)
	return n > 0, err
}

// AddNotice stores a complete notice (Missing is empty) and resolves what its URLs name.
// enteredBy is the service admin who entered an emailed notice ("" for the form).
func (s *Store) AddNotice(ctx context.Context, n Notice, ipHash, enteredBy string) (string, error) {
	source := "form"
	if enteredBy != "" {
		source = "email"
	}
	var t Target
	if ref, ok := OnMaktaba(n.URLs); ok {
		var err error
		if t, _, err = s.Resolve(ctx, ref); err != nil {
			return "", err
		}
	}
	urls := make([]string, 0, len(n.URLs))
	for _, u := range n.URLs {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `
        INSERT INTO moderation_notices (source, claimant_name, claimant_email, claimant_address, claimant_phone, work,
            urls, programme, at_time, explanation, signature, privacy, urgent, server_id, channel_id, publisher_id,
            ip_hash, entered_by)
        VALUES ($1, $2, lower($3), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
        RETURNING id::text`,
		source, strings.TrimSpace(n.ClaimantName), strings.TrimSpace(n.ClaimantEmail), strings.TrimSpace(n.ClaimantAddress),
		strings.TrimSpace(n.ClaimantPhone), strings.TrimSpace(n.Work), pq.Array(urls), strings.TrimSpace(n.Programme), n.At,
		strings.TrimSpace(n.Explanation), strings.TrimSpace(n.Signature), n.Privacy, n.Urgent,
		nullUUID(t.ServerID), nullUUID(t.ChannelID), nullUUID(t.PublisherID), ipHash, enteredBy).Scan(&id)
	if err != nil {
		return "", err
	}
	if n.Urgent {
		s.log().Error("moderation: an urgent copyright notice", "code", AlertUrgent, "notice_id", id)
	}
	return id, nil
}

// Report is a viewer's report from a watch surface.
type Report struct {
	Category   string     `json:"category"`
	About      string     `json:"about"`
	ChannelURL string     `json:"channel"`
	Programme  string     `json:"programme"`
	At         *time.Time `json:"at"`
	Email      string     `json:"email"`
	Body       string     `json:"text"`
}

// AlertUrgent is the error code an urgent report or notice logs: maktaba_errors_total counts it
// and deploy/cloud-ha/monitoring/alerts.yml pages the on-call admin (SEV2,
// docs/runbooks/cloud/broadcast-urgent-report.md).
const AlertUrgent = "moderation_urgent_report"

// AddReport stores a report; an urgent one pages the on-call admin.
func (s *Store) AddReport(ctx context.Context, r Report, ipHash string) (string, error) {
	ref, ok := ParseWatchURL(r.ChannelURL)
	if !ok {
		return "", bad("channel must be a watch.maktaba.tv channel")
	}
	t, _, err := s.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	about := r.About
	if about == "" {
		about = "content"
	}
	at := s.now()
	if r.At != nil {
		at = *r.At
	}
	urgent := Urgent(r.Category)
	var id string
	err = s.DB.QueryRowContext(ctx, `
        INSERT INTO moderation_reports (category, about, channel_url, server_id, channel_id, publisher_id, programme,
            at_time, email, body, urgent, ip_hash)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, lower($9), $10, $11, $12) RETURNING id::text`,
		r.Category, about, strings.TrimSpace(r.ChannelURL), nullUUID(t.ServerID), nullUUID(t.ChannelID), nullUUID(t.PublisherID),
		strings.TrimSpace(r.Programme), at, strings.TrimSpace(r.Email), strings.TrimSpace(r.Body), urgent, ipHash).Scan(&id)
	if err != nil {
		return "", err
	}
	if urgent {
		s.log().Error("moderation: an urgent report", "code", AlertUrgent, "report_id", id, "category", r.Category)
	}
	return id, nil
}

// audit writes one moderation_audit row.
func (s *Store) audit(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, by bc.Actor, verb, kind, id, reason string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	_, err := q.ExecContext(ctx, `INSERT INTO moderation_audit (actor_id, actor_email, verb, target_kind, target_id, reason, detail)
        VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`, by.ID, by.Email, verb, kind, id, reason, string(raw))
	return err
}

// TakedownRequest is a service admin's action on a notice, a report, an authority's order or what
// they saw themselves.
type TakedownRequest struct {
	Basis    string `json:"basis"`
	NoticeID string `json:"notice_id"`
	ReportID string `json:"report_id"`
	Scope    string `json:"scope"`
	// Channel is the channel's cloud id (defaults to what the notice or report resolved to).
	Channel string `json:"channel"`
	// PublisherID names the publisher for the publisher scope (defaults to the channel's).
	PublisherID string `json:"publisher_id"`
	// At is when the item aired (defaults to the notice's or report's time); ItemRef names it when
	// the guide gave one.
	At         *time.Time `json:"at"`
	ItemRef    string     `json:"item_ref"`
	SourceKind string     `json:"source_kind"`
	SourceRef  string     `json:"source_ref"`
	Programme  string     `json:"programme"`
	// AiringNow slates the channel at the edge at once, until AiringUntil (else SlateMax), or until
	// the home confirms the takedown.
	AiringNow   bool       `json:"airing_now"`
	AiringUntil *time.Time `json:"airing_until"`
	Reason      string     `json:"reason"`
}

// Action is one takedown or restore.
type Action struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Basis       string     `json:"basis"`
	Scope       string     `json:"scope"`
	NoticeID    string     `json:"notice_id,omitempty"`
	ReportID    string     `json:"report_id,omitempty"`
	Restores    string     `json:"restores,omitempty"`
	ServerID    string     `json:"server_id"`
	ChannelID   string     `json:"channel_id,omitempty"`
	PublisherID string     `json:"publisher_id,omitempty"`
	At          *time.Time `json:"at,omitempty"`
	ItemRef     string     `json:"item_ref,omitempty"`
	SourceKind  string     `json:"source_kind,omitempty"`
	SourceRef   string     `json:"source_ref,omitempty"`
	Programme   string     `json:"programme,omitempty"`
	Revocation  string     `json:"revocation,omitempty"`
	HomeState   string     `json:"home_state"`
	HomeDetail  string     `json:"home_detail,omitempty"`
	HomeAt      *time.Time `json:"home_at,omitempty"`
	Reason      string     `json:"reason"`
	Actor       string     `json:"actor"`
	CreatedAt   time.Time  `json:"created_at"`
	RestoredAt  *time.Time `json:"restored_at,omitempty"`
	// Strike and Level are what the takedown did to the publisher's standing (a takedown answer only).
	Strike bool   `json:"strike,omitempty"`
	Level  string `json:"level,omitempty"`
}

const actionCols = `a.id::text, a.kind, a.basis, a.scope, COALESCE(a.notice_id::text, ''), COALESCE(a.report_id::text, ''),
    COALESCE(a.restores::text, ''), a.server_id::text, COALESCE(a.channel_id::text, ''), COALESCE(a.publisher_id::text, ''),
    a.at_time, a.item_ref, a.source_kind, a.source_ref, a.programme, a.revocation_id, a.home_state, a.home_detail, a.home_at,
    a.reason, COALESCE(NULLIF(a.actor_email, ''), a.actor_id), a.created_at, a.restored_at`

func scanAction(row interface{ Scan(...any) error }) (Action, error) {
	var a Action
	var at, homeAt, restored sql.NullTime
	var rev sql.NullInt64
	err := row.Scan(&a.ID, &a.Kind, &a.Basis, &a.Scope, &a.NoticeID, &a.ReportID, &a.Restores, &a.ServerID, &a.ChannelID,
		&a.PublisherID, &at, &a.ItemRef, &a.SourceKind, &a.SourceRef, &a.Programme, &rev, &a.HomeState, &a.HomeDetail,
		&homeAt, &a.Reason, &a.Actor, &a.CreatedAt, &restored)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.At, a.HomeAt, a.RestoredAt = timePtr(at), timePtr(homeAt), timePtr(restored)
	if rev.Valid {
		a.Revocation = bc.EntryID(rev.Int64)
	}
	return a, err
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// action reads one action.
func (s *Store) action(ctx context.Context, id string) (Action, error) {
	return scanAction(s.DB.QueryRowContext(ctx, `SELECT `+actionCols+` FROM moderation_actions a WHERE a.id::text = $1`, id))
}

// revocationReason is the kill-switch reason a basis shows the publisher.
func revocationReason(basis string) string {
	if basis == BasisReport || basis == BasisAdminKnowledge {
		return bc.ReasonReport
	}
	return bc.ReasonNotice
}

type basisRow struct {
	target    Target
	at        *time.Time
	programme string
	claimant  string
	privacy   bool
}

// basisOf reads the notice or report an action answers (it must still be open).
func (s *Store) basisOf(ctx context.Context, req TakedownRequest) (basisRow, error) {
	var b basisRow
	var at sql.NullTime
	var status string
	switch req.Basis {
	case BasisNotice:
		if req.NoticeID == "" {
			return b, bad("notice_id is required for a notice")
		}
		err := s.DB.QueryRowContext(ctx, `SELECT status, COALESCE(server_id::text, ''), COALESCE(channel_id::text, ''),
            COALESCE(publisher_id::text, ''), at_time, programme, claimant_name, privacy
            FROM moderation_notices WHERE id::text = $1`, req.NoticeID).Scan(&status, &b.target.ServerID, &b.target.ChannelID,
			&b.target.PublisherID, &at, &b.programme, &b.claimant, &b.privacy)
		if errors.Is(err, sql.ErrNoRows) {
			return b, ErrNotFound
		}
		if err != nil {
			return b, err
		}
	case BasisReport:
		if req.ReportID == "" {
			return b, bad("report_id is required for a report")
		}
		err := s.DB.QueryRowContext(ctx, `SELECT status, COALESCE(server_id::text, ''), COALESCE(channel_id::text, ''),
            COALESCE(publisher_id::text, ''), at_time, programme
            FROM moderation_reports WHERE id::text = $1`, req.ReportID).Scan(&status, &b.target.ServerID, &b.target.ChannelID,
			&b.target.PublisherID, &at, &b.programme)
		if errors.Is(err, sql.ErrNoRows) {
			return b, ErrNotFound
		}
		if err != nil {
			return b, err
		}
	default:
		status = "open"
	}
	if status != "open" {
		return b, ErrClosed
	}
	b.at = timePtr(at)
	return b, nil
}

// Takedown carries out a service admin's action: the home's signed instruction (an item or a
// source), the edge's slate for an item airing now, or the kill switch for a channel or a
// publisher; the strike a valid notice gives; the statement of reasons; the audit. The third
// active strike ends the publisher's broadcasting.
func (s *Store) Takedown(ctx context.Context, req TakedownRequest, by bc.Actor) (Action, error) {
	req.Reason = strings.TrimSpace(req.Reason)
	switch {
	case !ValidBasis(req.Basis):
		return Action{}, bad("basis must be notice, report, authority or admin_knowledge")
	case !ValidScope(req.Scope):
		return Action{}, bad("scope must be item, source, channel or publisher")
	case req.Reason == "" || len(req.Reason) > 1000:
		return Action{}, bad("a reason (at most 1000 characters) is required")
	case req.Scope == ScopeSource && (!ValidSourceKind(req.SourceKind) || strings.TrimSpace(req.SourceRef) == ""):
		return Action{}, bad("a source takedown names source_kind and source_ref")
	}
	b, err := s.basisOf(ctx, req)
	if err != nil {
		return Action{}, err
	}
	t := b.target
	if req.Channel != "" {
		if t, err = s.channel(ctx, req.Channel); err != nil {
			return Action{}, err
		}
	} else if t.ChannelID != "" {
		if t, err = s.channel(ctx, t.ChannelID); err != nil {
			return Action{}, err
		}
	}
	if req.Scope == ScopePublisher && req.PublisherID != "" {
		t.PublisherID = req.PublisherID
	}
	if t.ServerID == "" || (req.Scope != ScopePublisher && t.ChannelID == "") || (req.Scope == ScopePublisher && t.PublisherID == "") {
		return Action{}, ErrNoTarget
	}
	at := req.At
	if at == nil {
		at = b.at
	}
	programme := strings.TrimSpace(req.Programme)
	if programme == "" {
		programme = b.programme
	}
	if req.Scope == ScopeItem && strings.TrimSpace(req.ItemRef) == "" && at == nil {
		return Action{}, bad("an item takedown names item_ref or the time it aired (at)")
	}

	// the kill switch's part: a programme's slate, a channel off air, a publisher suspended
	var entry *bc.Entry
	now := s.now()
	switch req.Scope {
	case ScopeItem, ScopeSource:
		if req.AiringNow {
			if t.EdgeChannel == "" {
				return Action{}, ErrEdgeUnknown
			}
			until := now.Add(SlateMax)
			if req.AiringUntil != nil && req.AiringUntil.After(now) && req.AiringUntil.Before(until) {
				until = *req.AiringUntil
			}
			entry = &bc.Entry{Kind: bc.KindProgramme, Ref: t.EdgeChannel, Action: bc.ActionSlate, Reason: revocationReason(req.Basis), Until: &until}
		}
	case ScopeChannel:
		if t.EdgeChannel == "" {
			return Action{}, ErrEdgeUnknown
		}
		entry = &bc.Entry{Kind: bc.KindChannel, Ref: t.EdgeChannel, Action: bc.ActionOffAir, Reason: revocationReason(req.Basis)}
	case ScopePublisher:
		entry = &bc.Entry{Kind: bc.KindPublisher, Ref: t.PublisherID, Action: bc.ActionSuspend, Reason: revocationReason(req.Basis)}
	}
	var revID any
	if entry != nil {
		row, _, err := s.Rev.Add(ctx, *entry, auditNote(req.Reason), by)
		if err != nil {
			return Action{}, err
		}
		n, _ := bc.ParseEntryID(row.ID)
		revID = n
	}

	homeState := "pending"
	if req.Scope == ScopeChannel || req.Scope == ScopePublisher {
		homeState = "none"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	if err := tx.QueryRowContext(ctx, `
        INSERT INTO moderation_actions (kind, basis, scope, notice_id, report_id, server_id, channel_id, publisher_id, at_time,
            item_ref, source_kind, source_ref, programme, revocation_id, home_state, reason, actor_id, actor_email)
        VALUES ('takedown', $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id::text`,
		req.Basis, req.Scope, nullUUID(req.NoticeID), nullUUID(req.ReportID), t.ServerID, nullUUID(t.ChannelID),
		nullUUID(t.PublisherID), at, strings.TrimSpace(req.ItemRef), sourceKind(req), strings.TrimSpace(req.SourceRef),
		programme, revID, homeState, req.Reason, by.ID, by.Email).Scan(&id); err != nil {
		return Action{}, err
	}
	switch req.Basis {
	case BasisNotice:
		if _, err := tx.ExecContext(ctx, `UPDATE moderation_notices SET status = 'accepted', decided_at = now(), decided_by = $2,
            decision_note = $3 WHERE id::text = $1`, req.NoticeID, by.ID, req.Reason); err != nil {
			return Action{}, err
		}
	case BasisReport:
		if _, err := tx.ExecContext(ctx, `UPDATE moderation_reports SET status = 'actioned', decided_at = now(), decided_by = $2,
            decision_note = $3 WHERE id::text = $1`, req.ReportID, by.ID, req.Reason); err != nil {
			return Action{}, err
		}
	}

	// the statement of reasons (DSA Art. 17): what, why, the claimant unless privacy was asked, how
	// to counter-notify
	claimant := b.claimant
	if b.privacy {
		claimant = ""
	}
	params := map[string]any{"programme": programme, "scope": req.Scope, "basis": req.Basis, "claimant": claimant,
		"notice_id": req.NoticeID, "source_kind": sourceKind(req), "source_ref": strings.TrimSpace(req.SourceRef)}
	if err := s.statement(ctx, tx, t, id, "takedown", params); err != nil {
		return Action{}, err
	}

	// the strike: a valid notice that led to a takedown, once per item per 30 days
	strike, level := false, LevelNone
	if req.Basis == BasisNotice && t.PublisherID != "" {
		dup, err := s.duplicateStrike(ctx, tx, t.PublisherID, t.ChannelID, id, req)
		if err != nil {
			return Action{}, err
		}
		if !dup {
			if _, err := tx.ExecContext(ctx, `INSERT INTO moderation_strikes (publisher_id, notice_id, action_id, at, expires_at)
                VALUES ($1, $2, $3, $4, $5)`, t.PublisherID, req.NoticeID, id, now, s.policy().Expiry(now)); err != nil {
				return Action{}, err
			}
			strike = true
		}
		active, err := s.activeStrikes(ctx, tx, t.PublisherID)
		if err != nil {
			return Action{}, err
		}
		level = s.policy().Level(active)
		if strike && level == LevelWarning {
			if err := s.statement(ctx, tx, t, id, "strike_warning", map[string]any{"count": active, "limit": s.policy().Limit}); err != nil {
				return Action{}, err
			}
		}
	}
	if err := s.audit(ctx, tx, by, "takedown", "action", id, req.Reason, map[string]any{"basis": req.Basis, "scope": req.Scope,
		"notice_id": req.NoticeID, "report_id": req.ReportID, "channel": t.ChannelID, "strike": strike, "level": level}); err != nil {
		return Action{}, err
	}
	if err := tx.Commit(); err != nil {
		return Action{}, err
	}
	s.log().Warn("moderation: takedown", "event", "moderation_takedown", "action_id", id, "basis", req.Basis, "scope", req.Scope,
		"server_id", t.ServerID, "strike", strike, "level", level, "staff_id", by.ID)
	if strike && level == LevelTerminate {
		if err := s.terminate(ctx, t, by); err != nil {
			s.log().Error("moderation: ending a repeat infringer's broadcasting failed", "err", err, "publisher_id", t.PublisherID)
		}
	}
	s.push(ctx)
	s.mailPublisher(ctx, t.PublisherID, "takedown", params)
	a, err := s.action(ctx, id)
	a.Strike, a.Level = strike, level
	return a, err
}

func sourceKind(req TakedownRequest) string {
	if req.Scope == ScopeSource {
		return req.SourceKind
	}
	return ""
}

// auditNote fits a reason into the kill switch's 500-character note.
func auditNote(reason string) string {
	if len(reason) > 500 {
		return reason[:500]
	}
	return reason
}

func (s *Store) push(ctx context.Context) {
	if s.Push != nil {
		s.Push(ctx)
	}
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// duplicateStrike: the same item (or source, or channel and time) already gave this publisher a
// strike in the last 30 days.
func (s *Store) duplicateStrike(ctx context.Context, q execer, publisher, channel, action string, req TakedownRequest) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
        SELECT count(*) FROM moderation_strikes st JOIN moderation_actions a ON a.id = st.action_id
         WHERE st.publisher_id::text = $1 AND a.id::text <> $2 AND st.at > $3
           AND a.scope = $4
           AND ((a.item_ref <> '' AND a.item_ref = $5)
             OR (a.source_ref <> '' AND a.source_kind = $6 AND lower(a.source_ref) = lower($7))
             OR (a.scope IN ('channel', 'publisher') AND COALESCE(a.channel_id::text, '') = $8))`,
		publisher, action, s.now().Add(-StrikeDedupe), req.Scope, strings.TrimSpace(req.ItemRef), sourceKind(req),
		strings.TrimSpace(req.SourceRef), channel).Scan(&n)
	return n > 0, err
}

func (s *Store) activeStrikes(ctx context.Context, q execer, publisher string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM moderation_strikes WHERE publisher_id::text = $1
        AND withdrawn_at IS NULL AND expires_at > $2`, publisher, s.now()).Scan(&n)
	return n, err
}

// statement records what the publisher is told, for every home of theirs that broadcasts on the
// channel's home (the home the action names).
func (s *Store) statement(ctx context.Context, q execer, t Target, action, kind string, params map[string]any) error {
	if t.PublisherID == "" || t.ServerID == "" {
		return nil
	}
	raw, _ := json.Marshal(params)
	_, err := q.ExecContext(ctx, `INSERT INTO moderation_statements (server_id, publisher_id, action_id, kind, params)
        VALUES ($1, $2, $3, $4, $5::jsonb)`, t.ServerID, t.PublisherID, nullUUID(action), kind, string(raw))
	return err
}

// terminate ends a repeat infringer's broadcasting: a publisher entry with terminate takes every
// channel off air, the termination is recorded (Broadcaster is set to cancel at the period's end
// by the owner at the payment provider, which the log line asks for), and the publisher is told.
func (s *Store) terminate(ctx context.Context, t Target, by bc.Actor) error {
	var live int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM moderation_terminations WHERE publisher_id::text = $1
        AND reinstated_at IS NULL`, t.PublisherID).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	row, _, err := s.Rev.Add(ctx, bc.Entry{Kind: bc.KindPublisher, Ref: t.PublisherID, Action: bc.ActionTerminate,
		Reason: bc.ReasonRepeatInfringer}, "repeat-infringer policy: limit of active strikes reached", by)
	if err != nil {
		return err
	}
	n, _ := bc.ParseEntryID(row.ID)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO moderation_terminations (publisher_id, revocation_id) VALUES ($1, $2)`,
		t.PublisherID, n); err != nil {
		return err
	}
	if err := s.statement(ctx, tx, t, "", "terminated", map[string]any{"limit": s.policy().Limit}); err != nil {
		return err
	}
	if err := s.audit(ctx, tx, by, "terminate", "publisher", t.PublisherID, "repeat-infringer policy", map[string]any{"revocation": row.ID}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Broadcaster is cancelled at the period's end with no refund for the period: the payment
	// provider is set by the owner (no provider takes the call yet), and guest sharing is turned
	// off with the publisher entry (the edge refuses every channel of theirs)
	s.log().Error("moderation: broadcasting ended for a repeat infringer; set Broadcaster to cancel at the period's end",
		"code", "moderation_terminated", "publisher_id", t.PublisherID)
	s.mailPublisher(ctx, t.PublisherID, "terminated", map[string]any{"limit": s.policy().Limit})
	return nil
}

// Instructions are a home's pending takedowns and restores of items and sources.
func (s *Store) Instructions(ctx context.Context, serverID string) ([]Instruction, error) {
	rs, err := s.DB.QueryContext(ctx, `SELECT a.id::text, a.kind, COALESCE(a.notice_id::text, ''), COALESCE(a.restores::text, ''), a.scope,
        COALESCE(a.channel_id::text, ''), a.at_time, a.item_ref, a.source_kind, a.source_ref
        FROM moderation_actions a WHERE a.server_id::text = $1 AND a.home_state = 'pending' AND a.scope IN ('item', 'source')
        ORDER BY a.created_at, a.id LIMIT 100`, serverID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []Instruction{}
	for rs.Next() {
		var in Instruction
		var at sql.NullTime
		if err := rs.Scan(&in.ID, &in.Kind, &in.NoticeID, &in.Restores, &in.Scope, &in.Channel, &at, &in.ItemRef, &in.SourceKind, &in.SourceRef); err != nil {
			return nil, err
		}
		in.At = timePtr(at)
		out = append(out, in)
	}
	return out, rs.Err()
}

// Acknowledge records a home's answer to an instruction. A takedown's programme slate is then
// shortened to the item's slot (the home says when it ends) or lifted when the item is not on air.
func (s *Store) Acknowledge(ctx context.Context, serverID, actionID string, ack Ack) error {
	a, err := s.action(ctx, actionID)
	if err != nil {
		return err
	}
	if a.ServerID != serverID {
		return ErrNotFound // another home's instruction is no business of this one
	}
	if a.HomeState != "pending" {
		return ErrClosed
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE moderation_actions SET home_state = $2, home_detail = $3, home_at = now(),
        item_ref = CASE WHEN item_ref = '' THEN $4 ELSE item_ref END,
        programme = CASE WHEN programme = '' THEN $5 ELSE programme END
        WHERE id::text = $1`, actionID, ack.Result, truncate(ack.Detail, 500), truncate(ack.ItemRef, 200), truncate(ack.Programme, 300)); err != nil {
		return err
	}
	if a.Kind == "takedown" && a.Revocation != "" && (a.Scope == ScopeItem || a.Scope == ScopeSource) && ack.Result != "failed" {
		if err := s.settleSlate(ctx, a, ack); err != nil {
			s.log().Warn("moderation: the programme slate was not settled", "err", err, "action_id", a.ID)
		}
	}
	s.log().Info("moderation: the home answered", "event", "moderation_home_ack", "action_id", actionID, "result", ack.Result)
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// settleSlate shortens or lifts a takedown's programme slate once the home confirmed: the new
// entry (until the item's slot ends) is added before the old one is lifted, so the channel never
// shows the item in between.
func (s *Store) settleSlate(ctx context.Context, a Action, ack Ack) error {
	old, ok := bc.ParseEntryID(a.Revocation)
	if !ok {
		return nil
	}
	var until sql.NullTime
	var ref, reason string
	var lifted sql.NullTime
	if err := s.DB.QueryRowContext(ctx, `SELECT until_at, ref, reason, lifted_at FROM broadcast_revocations WHERE id = $1`, old).
		Scan(&until, &ref, &reason, &lifted); err != nil {
		return err
	}
	if lifted.Valid {
		return nil
	}
	now := s.now()
	if ack.AiringUntil != nil && ack.AiringUntil.After(now) && until.Valid && ack.AiringUntil.Before(until.Time) {
		u := *ack.AiringUntil
		row, _, err := s.Rev.Add(ctx, bc.Entry{Kind: bc.KindProgramme, Ref: ref, Action: bc.ActionSlate, Reason: reason, Until: &u},
			"the home confirmed the takedown; the slate holds until the item's slot ends", System)
		if err != nil {
			return err
		}
		n, _ := bc.ParseEntryID(row.ID)
		if _, err := s.DB.ExecContext(ctx, `UPDATE moderation_actions SET revocation_id = $2 WHERE id::text = $1`, a.ID, n); err != nil {
			return err
		}
	} else if ack.AiringUntil != nil && ack.AiringUntil.After(now) {
		return nil // the slot ends after the slate: the slate stays as it is
	}
	if _, err := s.Rev.Lift(ctx, old, "the home confirmed the takedown", System); err != nil && !errors.Is(err, bc.ErrRevocationNotFound) {
		return err
	}
	s.push(ctx)
	return nil
}
