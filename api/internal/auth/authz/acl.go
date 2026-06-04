package authz

import (
	"context"
	"database/sql"
)

// ACLStore wraps the `library_acl` table introduced by migration 0030.
// It powers the JWT `lib[]` snapshot at login time (Story 10.13 AC-5)
// and the admin grant/revoke endpoints.
type ACLStore struct {
	DB *sql.DB
}

// LibrariesFor returns the library ids the user can read. Used by the
// JWT minter at issue time so the JWT carries the snapshot.
func (s *ACLStore) LibrariesFor(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT library_id FROM library_acl WHERE user_id = $1 ORDER BY library_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Grant inserts (user_id, library_id). Idempotent: a duplicate insert
// is a no-op (the (user_id, library_id) primary key handles it via ON
// CONFLICT).
func (s *ACLStore) Grant(ctx context.Context, userID, libraryID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO library_acl (user_id, library_id) VALUES ($1, $2)
		   ON CONFLICT (user_id, library_id) DO NOTHING`, userID, libraryID)
	return err
}

// Revoke removes (user_id, library_id). Returns nil even when the row
// did not exist — the caller wanted "this user no longer has this
// library", which is the post-condition either way.
func (s *ACLStore) Revoke(ctx context.Context, userID, libraryID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2`, userID, libraryID)
	return err
}

// Grant is one (user, library, role) row — the shape the admin ACL
// matrix reads and writes (web-pages-batch2, slot 0072 added `role`).
type Grant struct {
	UserID    string `json:"user_id"`
	LibraryID string `json:"library_id"`
	Role      string `json:"role"`
}

// AllGrants returns every (user, library, role) mapping, ordered for
// stable rendering. Powers GET /api/admin/library-acl.
func (s *ACLStore) AllGrants(ctx context.Context) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT user_id, library_id, role FROM library_acl ORDER BY user_id, library_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.UserID, &g.LibraryID, &g.Role); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetRole upserts the role for (user_id, library_id). It reuses the
// slot-0030 presence semantics (a row == at least read) while the
// slot-0072 `role` carries the finer level. Idempotent via the
// (user_id, library_id) primary key.
func (s *ACLStore) SetRole(ctx context.Context, userID, libraryID, role string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO library_acl (user_id, library_id, role) VALUES ($1, $2, $3)
		   ON CONFLICT (user_id, library_id) DO UPDATE SET role = EXCLUDED.role`,
		userID, libraryID, role)
	return err
}

// HasLibrary reports whether (user_id, library_id) exists. Cheaper
// than LibrariesFor when we only need a yes/no for one library.
func (s *ACLStore) HasLibrary(ctx context.Context, userID, libraryID string) (bool, error) {
	var exists bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM library_acl WHERE user_id = $1 AND library_id = $2)`,
		userID, libraryID,
	).Scan(&exists)
	return exists, err
}
