package stores

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
)

// ErrBadServerSecret: the server id and secret a home agent presented
// do not match a server.
var ErrBadServerSecret = errors.New("servers: bad server credentials")

// VerifySecret authenticates a home agent's server id + secret (the
// credentials claim redeem issued; argon2id at rest).
func (s *Servers) VerifySecret(ctx context.Context, serverID, secret string) error {
	if serverID == "" || secret == "" || !looksLikeUUID(serverID) {
		return ErrBadServerSecret
	}
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT server_secret_hash FROM servers WHERE id = $1`, serverID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBadServerSecret
	}
	if err != nil {
		return err
	}
	if argon2id.Verify(secret, hash) != nil {
		return ErrBadServerSecret
	}
	return nil
}

// ReplaceLinks sets the cloud accounts a home has linked (home_links) to
// exactly cloudUserIDs. Ids that are not cloud users are skipped.
func (s *Servers) ReplaceLinks(ctx context.Context, serverID string, cloudUserIDs []string) error {
	ids := make([]string, 0, len(cloudUserIDs))
	for _, id := range cloudUserIDs {
		if looksLikeUUID(id) {
			ids = append(ids, id)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM home_links WHERE server_id = $1 AND NOT (cloud_user_id = ANY($2::uuid[]))`,
		serverID, pq.Array(ids)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO home_links (server_id, cloud_user_id)
        SELECT $1, u.id FROM users u WHERE u.id = ANY($2::uuid[])
        ON CONFLICT (server_id, cloud_user_id) DO UPDATE SET reported_at = now()
    `, serverID, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}

// IsLinked reports whether a home has linked the cloud account.
func (s *Servers) IsLinked(ctx context.Context, serverID, cloudUserID string) (bool, error) {
	if !looksLikeUUID(serverID) || !looksLikeUUID(cloudUserID) {
		return false, nil
	}
	var one int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM home_links WHERE server_id = $1 AND cloud_user_id = $2`, serverID, cloudUserID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// LinkedHome is one home a cloud account reaches: linked by the home,
// owned (its cloud registration), or both.
type LinkedHome struct {
	Server
	Linked bool
	Owned  bool
	// Online: the home's tunnel is up (server_health.online, set by the
	// relay at connect and disconnect).
	Online bool
}

// HomesFor lists the homes a cloud account is linked to or owns.
func (s *Servers) HomesFor(ctx context.Context, cloudUserID string) ([]LinkedHome, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT s.id, s.owner_user_id, s.name, s.slug, s.plan, s.version, s.last_seen_at,
               s.direct_ip::text, s.direct_port, s.created_at,
               l.cloud_user_id IS NOT NULL, s.owner_user_id = $1, COALESCE(h.online, false)
          FROM servers s
          LEFT JOIN home_links l ON l.server_id = s.id AND l.cloud_user_id = $1
          LEFT JOIN server_health h ON h.server_id = s.id
         WHERE s.owner_user_id = $1 OR l.cloud_user_id IS NOT NULL
         ORDER BY s.name, s.created_at
    `, cloudUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkedHome{}
	for rows.Next() {
		var h LinkedHome
		if err := rows.Scan(&h.ID, &h.OwnerUserID, &h.Name, &h.Slug, &h.Plan, &h.Version, &h.LastSeenAt,
			&h.DirectIP, &h.DirectPort, &h.CreatedAt, &h.Linked, &h.Owned, &h.Online); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
