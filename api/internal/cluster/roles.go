package cluster

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/lib/pq"
)

// WorkerGroup is the group role every machine role joins (slot 0103).
const WorkerGroup = "maktaba_worker"

// Reasons the home cannot take machines.
const (
	ReasonSQLite       = "joining machines needs a Postgres home (this home runs on SQLite)"
	ReasonCannotCreate = "the database user cannot create roles"
	ReasonNoGroup      = "the maktaba_worker role is missing (run the migrations as a user that may create roles)"
)

// Roles provisions the machines' database roles (design §3.3).
type Roles interface {
	// Supported reports whether machines can be given roles, and why not.
	Supported(ctx context.Context) (bool, string)
	// DatabaseName is the home's own database.
	DatabaseName(ctx context.Context) (string, error)
	// Create makes (or re-enables) a LOGIN role in the worker group.
	Create(ctx context.Context, role, password string) error
	// Disable makes the role NOLOGIN, rotates its password and ends its
	// sessions where the home's user may (and the machine's copy of the
	// database stops following). It returns how many sessions were ended.
	Disable(ctx context.Context, role string) (int, error)
	// Drop removes the role and the machine's replication role (best
	// effort, when a machine is removed).
	Drop(ctx context.Context, role string) error
}

// Unsupported is Roles for a home that cannot take machines.
type Unsupported struct{ Reason string }

// Supported implements Roles.
func (u Unsupported) Supported(context.Context) (bool, string) { return false, u.Reason }

// DatabaseName implements Roles.
func (u Unsupported) DatabaseName(context.Context) (string, error) { return "", errors.New(u.Reason) }

// Create implements Roles.
func (u Unsupported) Create(context.Context, string, string) error { return errors.New(u.Reason) }

// Disable implements Roles.
func (u Unsupported) Disable(context.Context, string) (int, error) { return 0, nil }

// Drop implements Roles.
func (u Unsupported) Drop(context.Context, string) error { return nil }

var roleRe = regexp.MustCompile(`^maktaba_node_[0-9a-f]{32}$`)

// ValidRole reports whether role is a machine role name.
func ValidRole(role string) bool { return roleRe.MatchString(role) }

// NewPassword is 32 random bytes, base64url without padding.
func NewPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PGRoles provisions roles on the home's Postgres.
type PGRoles struct {
	DB *sql.DB
}

var _ Roles = (*PGRoles)(nil)

// Supported implements Roles: the current user is superuser or may create
// roles, and the maktaba_worker group exists.
func (p *PGRoles) Supported(ctx context.Context) (bool, string) {
	var may, group bool
	err := p.DB.QueryRowContext(ctx, `
SELECT COALESCE((SELECT rolsuper OR rolcreaterole FROM pg_roles WHERE rolname = current_user), false),
       EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, WorkerGroup).Scan(&may, &group)
	switch {
	case err != nil:
		return false, "cannot check the database user's privileges: " + err.Error()
	case !may:
		return false, ReasonCannotCreate
	case !group:
		return false, ReasonNoGroup
	}
	return true, ""
}

// DatabaseName implements Roles.
func (p *PGRoles) DatabaseName(ctx context.Context) (string, error) {
	var name string
	err := p.DB.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name)
	return name, err
}

// Create implements Roles. A role left over from an earlier membership
// (the machine was removed, the role could not be dropped) is re-enabled
// with the new password.
func (p *PGRoles) Create(ctx context.Context, role, password string) error {
	if !ValidRole(role) {
		return fmt.Errorf("cluster: invalid role name %q", role)
	}
	var exists bool
	if err := p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return err
	}
	id, pw, group := pq.QuoteIdentifier(role), pq.QuoteLiteral(password), pq.QuoteIdentifier(WorkerGroup)
	if !exists {
		_, err := p.DB.ExecContext(ctx, `CREATE ROLE `+id+` LOGIN PASSWORD `+pw+` IN ROLE `+group)
		return err
	}
	if _, err := p.DB.ExecContext(ctx, `ALTER ROLE `+id+` LOGIN PASSWORD `+pw); err != nil {
		return err
	}
	_, err := p.DB.ExecContext(ctx, `GRANT `+group+` TO `+id)
	return err
}

// Disable implements Roles.
func (p *PGRoles) Disable(ctx context.Context, role string) (int, error) {
	if !ValidRole(role) {
		return 0, fmt.Errorf("cluster: invalid role name %q", role)
	}
	pw, err := NewPassword()
	if err != nil {
		return 0, err
	}
	if _, err := p.DB.ExecContext(ctx,
		`ALTER ROLE `+pq.QuoteIdentifier(role)+` NOLOGIN PASSWORD `+pq.QuoteLiteral(pw)); err != nil {
		return 0, err
	}
	// its copy of this database stops following too (specs/epics/37-node-replica)
	p.endCopy(ctx, role, false)
	rows, err := p.DB.QueryContext(ctx, `SELECT pid FROM pg_stat_activity WHERE usename = $1`, role)
	if err != nil {
		return 0, nil // the role is off; the node notices at its next heartbeat
	}
	var pids []int
	for rows.Next() {
		var pid int
		if rows.Scan(&pid) == nil {
			pids = append(pids, pid)
		}
	}
	_ = rows.Close()
	ended := 0
	for _, pid := range pids {
		var ok bool
		// Not permitted (no pg_signal_backend): the node stops itself
		// when its heartbeat reads "revoked".
		if err := p.DB.QueryRowContext(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&ok); err == nil && ok {
			ended++
		}
	}
	return ended, nil
}

// Drop implements Roles.
func (p *PGRoles) Drop(ctx context.Context, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("cluster: invalid role name %q", role)
	}
	p.endCopy(ctx, role, true)
	_, err := p.DB.ExecContext(ctx, `DROP ROLE IF EXISTS `+pq.QuoteIdentifier(role))
	return err
}

// Usable reports whether role exists and may log in on this database's cluster now.
func (p *PGRoles) Usable(ctx context.Context, role string) (bool, error) {
	var can sql.NullBool
	err := p.DB.QueryRowContext(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, role).Scan(&can)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return can.Valid && can.Bool, err
}

// ServerPort is the port the database says it listens on (0 over a Unix socket).
func (p *PGRoles) ServerPort(ctx context.Context) (int, error) {
	var port sql.NullInt64
	if err := p.DB.QueryRowContext(ctx, `SELECT inet_server_port()`).Scan(&port); err != nil {
		return 0, err
	}
	return int(port.Int64), nil
}
