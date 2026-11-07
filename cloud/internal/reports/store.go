// Package reports keeps the problem reports homes send to Maktaba (Epic 53 story 53.16):
// a home's agent posts one (POST /v1/homes/{id}/reports, the server's secret), the
// service admin reads, triages and answers them (/v1/admin/reports), the home fetches the
// answers, and asks for all of its reports to be deleted. A report holds no title,
// library path, name, email or address (the home's export scrub), and is kept 90 days.
package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Retention is how long a report is kept.
const Retention = 90 * 24 * time.Hour

// Statuses a service admin sets.
var Statuses = map[string]bool{"new": true, "triaged": true, "fixed": true, "closed": true}

// ErrNotFound is a report that is not there.
var ErrNotFound = errors.New("reports: not found")

// Summary is a report in the list.
type Summary struct {
	ID           int64      `json:"id"`
	ServerID     string     `json:"server_id"`
	HomeName     string     `json:"home"`
	HomeReportID int        `json:"home_report_id"`
	ReceivedAt   time.Time  `json:"received_at"`
	HappenedAt   *time.Time `json:"happened_at,omitempty"`
	App          string     `json:"app"`
	Version      string     `json:"version"`
	Platform     string     `json:"platform"`
	Category     string     `json:"category"`
	Status       string     `json:"status"`
	FixedIn      string     `json:"fixed_in,omitempty"`
	IssueURL     string     `json:"issue_url,omitempty"`
	Reply        string     `json:"reply,omitempty"`
	RepliedAt    *time.Time `json:"replied_at,omitempty"`
	Screenshot   bool       `json:"screenshot"`
	Fingerprints []string   `json:"fingerprints"`
}

// Report is one report with its body.
type Report struct {
	Summary
	// the account that linked the home (so Maktaba can answer), never the sender
	OwnerEmail string          `json:"owner_email,omitempty"`
	Body       json.RawMessage `json:"body"`
}

// In is what a home sends.
type In struct {
	HomeReportID int
	HappenedAt   *time.Time
	App          string
	Version      string
	Platform     string
	Category     string
	Fingerprints []string
	Body         json.RawMessage
	Screenshot   []byte
}

// Filter narrows List; empty fields match everything.
type Filter struct {
	Version, App, Category, Status, Fingerprint string
	Limit, Offset                               int
}

// Reply is a service admin's answer, as its home fetches it.
type Reply struct {
	HomeReportID int       `json:"home_report_id"`
	Reply        string    `json:"reply"`
	RepliedAt    time.Time `json:"replied_at"`
	Status       string    `json:"status"`
	FixedIn      string    `json:"fixed_in,omitempty"`
}

// Store is the problem_reports table (slot 0053).
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Add keeps a report and answers its id. The same home report sent again (an answer the
// home never got) is the same row: its id comes back and nothing changes.
func (s *Store) Add(ctx context.Context, serverID string, in In) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `
        INSERT INTO problem_reports (server_id, home_report_id, received_at, happened_at, app, version, platform,
            category, fingerprints, body, screenshot)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        ON CONFLICT (server_id, home_report_id) DO NOTHING
        RETURNING id`,
		serverID, in.HomeReportID, s.now().UTC(), in.HappenedAt, in.App, in.Version, in.Platform, in.Category,
		pq.Array(nonNil(in.Fingerprints)), []byte(in.Body), nullBytes(in.Screenshot)).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM problem_reports WHERE server_id = $1 AND home_report_id = $2`,
		serverID, in.HomeReportID).Scan(&id)
	return id, false, err
}

const summaryCols = `p.id, p.server_id, COALESCE(sv.name, ''), p.home_report_id, p.received_at, p.happened_at, p.app,
    p.version, p.platform, p.category, p.status, p.fixed_in, p.issue_url, p.reply, p.replied_at,
    p.screenshot IS NOT NULL, p.fingerprints`

func scanSummary(row interface{ Scan(...any) error }, extra ...any) (Summary, error) {
	var x Summary
	var happened, replied sql.NullTime
	args := append([]any{&x.ID, &x.ServerID, &x.HomeName, &x.HomeReportID, &x.ReceivedAt, &happened, &x.App,
		&x.Version, &x.Platform, &x.Category, &x.Status, &x.FixedIn, &x.IssueURL, &x.Reply, &replied,
		&x.Screenshot, pq.Array(&x.Fingerprints)}, extra...)
	if err := row.Scan(args...); err != nil {
		return x, err
	}
	if happened.Valid {
		t := happened.Time
		x.HappenedAt = &t
	}
	if replied.Valid {
		t := replied.Time
		x.RepliedAt = &t
	}
	x.Fingerprints = nonNil(x.Fingerprints)
	return x, nil
}

// List is the reports across homes, newest first, and how many match.
func (s *Store) List(ctx context.Context, f Filter) ([]Summary, int, error) {
	where, args := []string{"TRUE"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Version != "" {
		add("p.version = $%d", f.Version)
	}
	if f.App != "" {
		add("p.app = $%d", f.App)
	}
	if f.Category != "" {
		add("p.category = $%d", f.Category)
	}
	if f.Status != "" {
		add("p.status = $%d", f.Status)
	}
	if f.Fingerprint != "" {
		add("$%d = ANY(p.fingerprints)", f.Fingerprint)
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM problem_reports p WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args = append(args, limit, max(f.Offset, 0))
	rows, err := s.DB.QueryContext(ctx, `SELECT `+summaryCols+` FROM problem_reports p
        LEFT JOIN servers sv ON sv.id = p.server_id
        WHERE `+cond+fmt.Sprintf(` ORDER BY p.received_at DESC, p.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		x, err := scanSummary(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}

// Get is one report with its body.
func (s *Store) Get(ctx context.Context, id int64) (Report, error) {
	var rep Report
	var body []byte
	var owner sql.NullString
	x, err := scanSummary(s.DB.QueryRowContext(ctx, `SELECT `+summaryCols+`, p.body, u.email FROM problem_reports p
        LEFT JOIN servers sv ON sv.id = p.server_id
        LEFT JOIN users u ON u.id = sv.owner_user_id
        WHERE p.id = $1`, id), &body, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return rep, ErrNotFound
	}
	if err != nil {
		return rep, err
	}
	rep.Summary, rep.Body, rep.OwnerEmail = x, body, owner.String
	return rep, nil
}

// Screenshot is a report's screenshot (nil: none).
func (s *Store) Screenshot(ctx context.Context, id int64) ([]byte, error) {
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT screenshot FROM problem_reports WHERE id = $1`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// Execer is a database or a transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func one(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus sets a report's status (and the version a fix is in).
func (s *Store) SetStatus(ctx context.Context, x Execer, id int64, status, fixedIn string) error {
	return one(x.ExecContext(ctx, `UPDATE problem_reports SET status = $2, fixed_in = $3 WHERE id = $1`, id, status, fixedIn))
}

// SetIssue links a report to an issue (a GitHub issue's address, pasted by hand).
func (s *Store) SetIssue(ctx context.Context, x Execer, id int64, url string) error {
	return one(x.ExecContext(ctx, `UPDATE problem_reports SET issue_url = $2 WHERE id = $1`, id, url))
}

// SetReply is the service admin's answer to the home (its admin and the report's sender).
func (s *Store) SetReply(ctx context.Context, x Execer, id int64, reply string) error {
	return one(x.ExecContext(ctx, `UPDATE problem_reports SET reply = $2, replied_at = $3 WHERE id = $1`, id, reply, s.now().UTC()))
}

// RepliesSince is a home's answered reports, answered after since (oldest first).
func (s *Store) RepliesSince(ctx context.Context, serverID string, since time.Time) ([]Reply, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT home_report_id, reply, replied_at, status, fixed_in FROM problem_reports
        WHERE server_id = $1 AND replied_at IS NOT NULL AND replied_at > $2 ORDER BY replied_at LIMIT 200`, serverID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reply{}
	for rows.Next() {
		var r Reply
		if err := rows.Scan(&r.HomeReportID, &r.Reply, &r.RepliedAt, &r.Status, &r.FixedIn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteHome deletes every report of a home (its admin's "Delete what Maktaba has").
func (s *Store) DeleteHome(ctx context.Context, serverID string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM problem_reports WHERE server_id = $1`, serverID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Sweep deletes the reports received before the retention.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM problem_reports WHERE received_at < $1`, s.now().Add(-Retention))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
