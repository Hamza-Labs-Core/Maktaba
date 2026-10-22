package clusterauth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
)

// Copy answers a full node's credentials from its own copy of the home's database
// (nodeauth.Local, specs/epics/37-node-replica): the home's own lookups (Resolve, the
// sign-in's password check) on a Home whose stores read the copy. The copy is read
// only: nothing here writes (the last-seen marks Resolve tries are best-effort, and a
// failed sign-in is counted by the node, not in the copy).
type Copy struct {
	Home *Home
}

var _ nodeauth.Local = Copy{}

// Introspect implements nodeauth.Local: a token as the home finds it (by its hash), a
// web session by the SHA-256 of its id (web_session_keys: a copy holds no session id,
// which is a live cookie).
func (c Copy) Introspect(ctx context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	if req.Session != "" {
		return c.session(ctx, req.Session)
	}
	a, err := c.Home.Resolve(ctx, req)
	var no *Refusal
	if errors.As(err, &no) {
		return nil, nodeauth.ErrNotLive
	}
	return a, err
}

// session is a web session on the copy, by its id's hash.
func (c Copy) session(ctx context.Context, id string) (*delegation.IntrospectAnswer, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, nodeauth.ErrNotLive
	}
	var (
		userID, csrf string
		expires      time.Time
		revoked      sql.NullTime
		origin       sql.NullString
	)
	err = c.Home.DB.QueryRowContext(ctx, `SELECT CAST(user_id AS TEXT), csrf_sha256, expires_at, revoked_at,
	        CAST(origin_node_id AS TEXT) FROM web_session_keys WHERE id_sha256 = $1`, hexSum(u.String())).
		Scan(&userID, &csrf, &expires, &revoked, &origin)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nodeauth.ErrNotLive
	case err != nil:
		return nil, err
	case revoked.Valid || !expires.After(c.Home.now()):
		return nil, nodeauth.ErrNotLive
	}
	p, err := c.Home.livePrincipal(ctx, userID)
	var no *Refusal
	if errors.As(err, &no) {
		return nil, nodeauth.ErrNotLive
	}
	if err != nil {
		return nil, err
	}
	a := &delegation.IntrospectAnswer{Principal: p, Source: string(principal.SourceCookie), CSRFSHA256: csrf,
		ExpiresAt: expires}
	if origin.Valid && origin.String != "" {
		o := origin.String
		a.OriginNodeID = &o
	}
	return a, nil
}

// Principal implements nodeauth.Local.
func (c Copy) Principal(ctx context.Context, userID string) (*delegation.Principal, error) {
	p, err := c.Home.principalFor(ctx, userID)
	if errors.Is(err, users.ErrNotFound) {
		return nil, nodeauth.ErrNotLive
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SignIn implements nodeauth.Local: the home's sign-in check (handlers/auth Login),
// without its writes.
func (c Copy) SignIn(ctx context.Context, username, password string) (*delegation.Principal, error) {
	u, err := c.Home.Users.GetByUsername(ctx, username)
	if errors.Is(err, users.ErrNotFound) {
		return nil, nodeauth.ErrNotLive
	}
	if err != nil {
		return nil, err
	}
	if u.IsLocked(c.Home.now()) {
		return nil, nodeauth.ErrLocked
	}
	if u.VerifyPassword(password) != nil {
		return nil, nodeauth.ErrNotLive
	}
	return c.Principal(ctx, u.ID)
}

// Synced implements nodeauth.Local: the home's beat as the copy last applied it
// (cluster_replica_beat, slot 0146).
func (c Copy) Synced(ctx context.Context) (time.Time, error) {
	var at time.Time
	err := c.Home.DB.QueryRowContext(ctx, `SELECT at FROM cluster_replica_beat WHERE id = 1`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return at, err
}

// NewCopy is a Copy over a node's copy of the home's database.
func NewCopy(db *sql.DB) Copy {
	return Copy{Home: &Home{DB: db, Sessions: sessions.New(db), Users: users.New(db),
		ACL: &authz.ACLStore{DB: db}, PAT: pat.New(db)}}
}
