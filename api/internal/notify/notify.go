// Package notify is the in-app notification store and fan-out (Epic 33, story 33.13;
// README decision 8: a general notifications table, TV changes its first user).
//
// Notify(kind, payload) is the one function producers call. A Hub hands every notice to
// its subscribers: the database sink (Store: the bell in the web header reads it) is the
// first; e-mail, mobile push (slot 0065 categories) and the general review flow
// subscribe later without changing a producer. The Hub drops a repeat of the same kind
// about the same subject inside its window (once an hour by default: story 33.13 EC1).
//
// A notice carries a message key and params, never text: the web app localises it
// (en / ar, story 33.13 AC5).
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Severities.
const (
	SeverityInfo    = "info"
	SeveritySuccess = "success"
	SeverityWarn    = "warn"
	SeverityError   = "error"
)

// AudienceAdmins is every admin; any other audience is one user id.
const AudienceAdmins = "admins"

// DefaultEvery is the window a repeat of a kind about a subject is dropped in.
const DefaultEvery = time.Hour

// Payload is what a producer says about a notice.
type Payload struct {
	Severity string
	// TitleKey is the web app's message key (i18n), e.g. "notify.tv.change.ready".
	TitleKey string
	Params   map[string]any
	// Link is a path in the web app ("/admin/live-tv/changes/<id>").
	Link string
	// Audience: AudienceAdmins (default) or a user id.
	Audience string
	// Subject is what the notice is about ("tv_change:<id>"): repeats are limited per
	// (kind, subject).
	Subject string
	// Every is the repeat window (0: DefaultEvery; negative: never limited).
	Every time.Duration
}

// Notification is one stored notice.
type Notification struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Severity  string         `json:"severity"`
	TitleKey  string         `json:"title_key"`
	Params    map[string]any `json:"params"`
	Link      string         `json:"link"`
	Audience  string         `json:"audience"`
	Subject   string         `json:"subject,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	Read      bool           `json:"read"`
}

// Notifier is the interface producers depend on (story 33.13 AC4).
type Notifier interface {
	Notify(ctx context.Context, kind string, p Payload) error
}

// Subscriber receives every notice the Hub lets through.
type Subscriber interface {
	Deliver(ctx context.Context, n Notification) error
}

// SubscriberFunc adapts a function.
type SubscriberFunc func(ctx context.Context, n Notification) error

// Deliver calls f.
func (f SubscriberFunc) Deliver(ctx context.Context, n Notification) error { return f(ctx, n) }

// Limiter answers whether a notice of kind about subject was made since a time.
type Limiter interface {
	SeenSince(ctx context.Context, kind, subject string, since time.Time) (bool, error)
}

// Hub fans a notice out to its subscribers.
type Hub struct {
	mu      sync.RWMutex
	subs    []Subscriber
	Limiter Limiter
	Now     func() time.Time
	Logger  *slog.Logger
}

// NewHub is a hub over a database: its Store is the first subscriber and the limiter.
func NewHub(db *sql.DB, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	st := &Store{DB: db}
	h := &Hub{Limiter: st, Logger: logger}
	h.Subscribe(st)
	return h
}

// Subscribe adds a subscriber (e-mail, push, the review flow).
func (h *Hub) Subscribe(s Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs = append(h.subs, s)
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

// Notify makes a notice and delivers it. A repeat inside the window is dropped (nil).
// Every subscriber is tried; the first error is returned.
func (h *Hub) Notify(ctx context.Context, kind string, p Payload) error {
	if h == nil {
		return nil
	}
	n := Notification{ID: uuid.NewString(), Kind: kind, Severity: p.Severity, TitleKey: p.TitleKey,
		Params: p.Params, Link: p.Link, Audience: p.Audience, Subject: p.Subject, CreatedAt: h.now()}
	if n.Severity == "" {
		n.Severity = SeverityInfo
	}
	if n.Audience == "" {
		n.Audience = AudienceAdmins
	}
	if n.TitleKey == "" {
		n.TitleKey = "notify." + kind
	}
	if n.Params == nil {
		n.Params = map[string]any{}
	}
	every := p.Every
	if every == 0 {
		every = DefaultEvery
	}
	if every > 0 && n.Subject != "" && h.Limiter != nil {
		seen, err := h.Limiter.SeenSince(ctx, kind, n.Subject, n.CreatedAt.Add(-every))
		if err != nil {
			return err
		}
		if seen {
			return nil
		}
	}
	h.mu.RLock()
	subs := append([]Subscriber(nil), h.subs...)
	h.mu.RUnlock()
	var first error
	for _, s := range subs {
		if err := s.Deliver(ctx, n); err != nil {
			if h.Logger != nil {
				h.Logger.Warn("notification not delivered", "event", "notify_deliver_failed",
					"kind", kind, "error", err.Error())
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// Store is the notifications table (slot 0130).
type Store struct {
	DB *sql.DB
}

// IsSQLite reports whether db is the SQLite backend.
func IsSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

// ts is a timestamp parameter: native on Postgres, the column default's text on SQLite.
func ts(db *sql.DB, t time.Time) any {
	if IsSQLite(db) {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}

// Deliver stores the notice (the Store is a Subscriber).
func (s *Store) Deliver(ctx context.Context, n Notification) error {
	params, err := json.Marshal(n.Params)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO notifications (id, kind, severity, title_key, params, link,
		audience, subject, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		n.ID, n.Kind, n.Severity, n.TitleKey, string(params), n.Link, n.Audience, n.Subject, ts(s.DB, n.CreatedAt))
	return err
}

// SeenSince implements Limiter.
func (s *Store) SeenSince(ctx context.Context, kind, subject string, since time.Time) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE kind = $1 AND subject = $2
		AND created_at >= $3`, kind, subject, ts(s.DB, since)).Scan(&n)
	return n > 0, err
}

// audience is the WHERE part that keeps a user to the notices addressed to them.
func audience(admin bool) string {
	if admin {
		return `(n.audience = 'admins' OR n.audience = $1)`
	}
	return `n.audience = $1`
}

// List is the user's notices, newest first, with each one's read flag.
func (s *Store) List(ctx context.Context, userID string, admin bool, limit int, unreadOnly bool) ([]Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	q := `SELECT n.id, n.kind, n.severity, n.title_key, n.params, n.link, n.audience, n.subject, n.created_at,
		CASE WHEN r.notification_id IS NULL THEN 0 ELSE 1 END
		FROM notifications n
		LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = $1
		WHERE ` + audience(admin)
	if unreadOnly {
		q += ` AND r.notification_id IS NULL`
	}
	q += ` ORDER BY n.created_at DESC, n.id DESC LIMIT $2`
	rows, err := s.DB.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var params []byte
		var at timeScan
		var read int
		if err := rows.Scan(&n.ID, &n.Kind, &n.Severity, &n.TitleKey, &params, &n.Link, &n.Audience,
			&n.Subject, &at, &read); err != nil {
			return nil, err
		}
		n.Params = map[string]any{}
		if len(params) > 0 {
			_ = json.Unmarshal(params, &n.Params)
		}
		n.CreatedAt, n.Read = at.t, read == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

// Unread counts the user's unread notices.
func (s *Store) Unread(ctx context.Context, userID string, admin bool) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications n WHERE `+audience(admin)+`
		AND NOT EXISTS (SELECT 1 FROM notification_reads r WHERE r.notification_id = n.id AND r.user_id = $1)`,
		userID).Scan(&n)
	return n, err
}

// ErrNotFound is a notice the user cannot see.
var ErrNotFound = errors.New("notify: not found")

// MarkRead marks one notice read for the user (idempotent).
func (s *Store) MarkRead(ctx context.Context, userID string, admin bool, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications n WHERE n.id = $2 AND `+audience(admin),
		userID, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notification_reads (notification_id, user_id)
		VALUES ($1, $2) ON CONFLICT (notification_id, user_id) DO NOTHING`, id, userID)
	return err
}

// MarkAllRead marks every notice the user sees read.
func (s *Store) MarkAllRead(ctx context.Context, userID string, admin bool) (int64, error) {
	// the read time is the column default (now)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, CAST($1 AS TEXT) FROM notifications n WHERE `+audience(admin)+`
		AND NOT EXISTS (SELECT 1 FROM notification_reads r WHERE r.notification_id = n.id AND r.user_id = $1)
		ON CONFLICT (notification_id, user_id) DO NOTHING`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// timeScan reads a native or text timestamp.
type timeScan struct{ t time.Time }

func (s *timeScan) Scan(v any) error {
	switch x := v.(type) {
	case time.Time:
		s.t = x.UTC()
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				s.t = t.UTC()
				return nil
			}
		}
	case []byte:
		return s.Scan(string(x))
	}
	return nil
}
