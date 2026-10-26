// Package audit writes and reads the cloud's audit log: who did what to
// which account. Service-admin actions, denied admin attempts, and the
// account events the stories name (verification, password reset,
// deletion, suspension) all land here.
//
// Rows carry no foreign key: an entry about an account outlives the
// account, and the actor is copied by id and email at write time.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Actions recorded by the account side. Free-form strings are allowed
// but these are the ones the console filters on.
const (
	ActionAdminDenied     = "admin.access_denied"
	ActionAdminViewUser   = "admin.account.view"
	ActionAdminSuspend    = "admin.account.suspend"
	ActionAdminUnsuspend  = "admin.account.unsuspend"
	ActionAdminBlock      = "admin.abuse.block"
	ActionAdminUnblock    = "admin.abuse.unblock"
	ActionAdminErase      = "admin.privacy.erase"
	ActionRoleGrant       = "role.grant"
	ActionRoleRevoke      = "role.revoke"
	ActionEmailVerified   = "auth.email_verified"
	ActionPasswordReset   = "auth.password_reset"
	ActionPasswordChange  = "account.password_change"
	ActionRefreshReplay   = "auth.refresh_replay"
	ActionAccountDelete   = "account.delete"
	ActionDeletionCancel  = "account.delete_cancel"
	ActionDisputeSuspend  = "billing.dispute_suspend"
	ActionSessionsRevoked = "account.sessions_revoked"
)

// Entry is one audit row.
type Entry struct {
	ID         int64          `json:"id"`
	ActorID    string         `json:"actor_id,omitempty"`
	ActorEmail string         `json:"actor_email,omitempty"`
	Action     string         `json:"action"`
	TargetKind string         `json:"target_kind,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	Detail     map[string]any `json:"detail"`
	IP         string         `json:"ip,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Log persists audit entries.
type Log struct {
	DB *sql.DB
}

// New wires the log to a pool.
func New(db *sql.DB) *Log { return &Log{DB: db} }

// execer is the part of *sql.DB and *sql.Tx Write needs, so an audit row
// can share the transaction of the change it records.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Write records e. Callers treat a failure as a failure of the action
// being audited when the action is an admin one; see WriteTx.
func (l *Log) Write(ctx context.Context, e Entry) error {
	return WriteTx(ctx, l.DB, e)
}

// WriteTx records e on the given connection or transaction.
func WriteTx(ctx context.Context, x execer, e Entry) error {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	dj, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(ctx, `
        INSERT INTO audit_log (actor_id, actor_email, action, target_kind, target_id, detail, ip)
        VALUES (NULLIF($1,'')::uuid, NULLIF($2,''), $3, NULLIF($4,''), NULLIF($5,''), $6, NULLIF($7,''))
    `, e.ActorID, e.ActorEmail, e.Action, e.TargetKind, e.TargetID, dj, e.IP)
	return err
}

// Filter narrows List. Empty fields match everything; Action matches as
// a prefix so "admin." finds every admin action.
type Filter struct {
	ActorID  string
	Action   string
	TargetID string
	Limit    int
	Offset   int
}

// MaxPage is the largest page List returns.
const MaxPage = 50

// List returns matching entries newest first and the total match count.
func (l *Log) List(ctx context.Context, f Filter) ([]Entry, int, error) {
	if f.Limit <= 0 || f.Limit > MaxPage {
		f.Limit = MaxPage
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "$?", "$"+strconv.Itoa(len(args))))
	}
	if f.ActorID != "" {
		add("actor_id::text = $?", f.ActorID)
	}
	if f.Action != "" {
		add("action LIKE $? || '%'", f.Action)
	}
	if f.TargetID != "" {
		add("target_id = $?", f.TargetID)
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := l.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := l.DB.QueryContext(ctx, `
        SELECT id, COALESCE(actor_id::text,''), COALESCE(actor_email,''), action,
               COALESCE(target_kind,''), COALESCE(target_id,''), detail, COALESCE(ip,''), created_at
        FROM audit_log WHERE `+cond+`
        ORDER BY created_at DESC, id DESC
        LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var dj []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.Action, &e.TargetKind, &e.TargetID, &dj, &e.IP, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal(dj, &e.Detail)
		out = append(out, e)
	}
	return out, total, rows.Err()
}
