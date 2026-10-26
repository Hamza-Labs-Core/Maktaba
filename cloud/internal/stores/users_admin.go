package stores

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// AccountSummary is one row of the service-admin account search.
type AccountSummary struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name,omitempty"`
	Plan        string     `json:"plan"`
	Status      string     `json:"status"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	Servers     int        `json:"servers"`
}

// AccountQuery narrows Search. Q matches email or display name
// (case-insensitive substring) or an exact account id.
type AccountQuery struct {
	Q      string
	Status string
	Plan   string
	Limit  int
	Offset int
}

// Search lists accounts for the operator console, newest first.
func (s *Users) Search(ctx context.Context, q AccountQuery) ([]AccountSummary, int, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	where := []string{"true"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if t := strings.TrimSpace(q.Q); t != "" {
		like := "%" + escapeLike(strings.ToLower(t)) + "%"
		p := arg(like)
		where = append(where, "(lower(u.email) LIKE "+p+" OR lower(COALESCE(u.display_name,'')) LIKE "+p+" OR u.id::text = "+arg(t)+")")
	}
	if q.Status != "" {
		where = append(where, "u.status = "+arg(q.Status))
	}
	if q.Plan != "" {
		where = append(where, "u.plan = "+arg(q.Plan))
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	lim, off := arg(q.Limit), arg(q.Offset)
	rows, err := s.DB.QueryContext(ctx, `
        SELECT u.id, u.email, COALESCE(u.display_name,''), u.plan, u.status, u.role, u.created_at, u.last_login_at,
               (SELECT count(*) FROM servers sv WHERE sv.owner_user_id = u.id)
        FROM users u WHERE `+cond+`
        ORDER BY u.created_at DESC, u.id
        LIMIT `+lim+` OFFSET `+off, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AccountSummary{}
	for rows.Next() {
		var a AccountSummary
		var last sql.NullTime
		if err := rows.Scan(&a.ID, &a.Email, &a.DisplayName, &a.Plan, &a.Status, &a.Role, &a.CreatedAt, &last, &a.Servers); err != nil {
			return nil, 0, err
		}
		if last.Valid {
			t := last.Time
			a.LastLoginAt = &t
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// SetStatus suspends or reinstates an account. Suspending records when
// and why; reinstating clears both.
func (s *Users) SetStatus(ctx context.Context, x interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, id, status, reason string) error {
	var res sql.Result
	var err error
	if status == StatusSuspended {
		res, err = x.ExecContext(ctx, `UPDATE users SET status = 'suspended', suspended_at = now(), suspended_reason = $2, updated_at = now() WHERE id = $1`, id, reason)
	} else {
		res, err = x.ExecContext(ctx, `UPDATE users SET status = 'active', suspended_at = NULL, suspended_reason = NULL, updated_at = now() WHERE id = $1`, id)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetRole grants or revokes the service-admin role. Only the operator
// CLI calls this; no HTTP route reaches it.
func (s *Users) SetRole(ctx context.Context, id, role string) error {
	if role != RoleUser && role != RoleServiceAdmin {
		return errors.New("users: unknown role " + role)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET role = $2, updated_at = now() WHERE id = $1`, id, role)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ServiceAdmins lists the accounts holding the service-admin role.
func (s *Users) ServiceAdmins(ctx context.Context) ([]AccountSummary, error) {
	res, _, err := s.Search(ctx, AccountQuery{Limit: 100})
	if err != nil {
		return nil, err
	}
	out := []AccountSummary{}
	for _, a := range res {
		if a.Role == RoleServiceAdmin {
			out = append(out, a)
		}
	}
	return out, nil
}

// Identity is one linked sign-in provider.
type Identity struct {
	Provider  string    `json:"provider"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Identities lists the OAuth providers linked to an account.
func (s *Users) Identities(ctx context.Context, id string) ([]Identity, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT provider, COALESCE(email,''), created_at FROM oauth_links WHERE user_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Identity{}
	for rows.Next() {
		var i Identity
		if err := rows.Scan(&i.Provider, &i.Email, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Deletion is a pending account-deletion hold.
type Deletion struct {
	RequestedAt time.Time `json:"requested_at"`
	PurgeAfter  time.Time `json:"purge_after"`
}

// PendingDeletion returns the account's live deletion hold, if any.
func (s *Users) PendingDeletion(ctx context.Context, id string) (*Deletion, error) {
	var d Deletion
	err := s.DB.QueryRowContext(ctx, `SELECT requested_at, purge_after FROM account_deletions WHERE user_id = $1 AND cancelled_at IS NULL`, id).Scan(&d.RequestedAt, &d.PurgeAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// RequestDeletion starts (or restarts) the 30-day deletion hold.
func (s *Users) RequestDeletion(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO account_deletions (user_id, purge_after)
        VALUES ($1, now() + INTERVAL '30 days')
        ON CONFLICT (user_id) DO UPDATE SET requested_at = now(), purge_after = now() + INTERVAL '30 days', cancelled_at = NULL
    `, id)
	return err
}

// CancelDeletion lifts a pending hold. It reports whether one was live.
func (s *Users) CancelDeletion(ctx context.Context, id string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE account_deletions SET cancelled_at = now() WHERE user_id = $1 AND cancelled_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
