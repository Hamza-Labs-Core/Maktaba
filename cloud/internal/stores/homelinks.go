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
	_, err := s.VerifyHome(ctx, serverID, secret)
	return err
}

// VerifyHome is VerifySecret, and says whether the home's owner is active. The first time a
// home uses its secret ends the retry of the claim that made it (RedeemClaim): it has its
// answer, so nothing may issue another secret. That is written before the caller is let in; when
// it can't be, the home is refused and tries again.
func (s *Servers) VerifyHome(ctx context.Context, serverID, secret string) (active bool, err error) {
	if serverID == "" || secret == "" || !looksLikeUUID(serverID) {
		return false, ErrBadServerSecret
	}
	var hash string
	var retry bool
	err = s.DB.QueryRowContext(ctx, `
        SELECT s.server_secret_hash, u.status = 'active',
               EXISTS (SELECT 1 FROM server_claims c WHERE c.used_server_id = s.id AND c.retry_key_hash IS NOT NULL AND c.expires_at > now())
          FROM servers s JOIN users u ON u.id = s.owner_user_id WHERE s.id = $1`, serverID).Scan(&hash, &active, &retry)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrBadServerSecret
	}
	if err != nil {
		return false, err
	}
	if argon2id.Verify(secret, hash) != nil {
		return false, ErrBadServerSecret
	}
	if retry {
		if err := s.endClaimRetry(ctx, serverID, hash); err != nil {
			return false, err
		}
	}
	return active, nil
}

// endClaimRetry ends the retry of serverID's claim, whose secret's hash was just checked. A retry
// that was under way (it holds the claim's row) has replaced that secret: ErrBadServerSecret.
func (s *Servers) endClaimRetry(ctx context.Context, serverID, hash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM server_claims WHERE used_server_id = $1 AND retry_key_hash IS NOT NULL FOR UPDATE`, serverID); err != nil {
		return err
	}
	// read after the lock, in a statement of its own
	var now string
	if err := tx.QueryRowContext(ctx, `SELECT server_secret_hash FROM servers WHERE id = $1`, serverID).Scan(&now); errors.Is(err, sql.ErrNoRows) {
		return ErrBadServerSecret
	} else if err != nil {
		return err
	}
	if now != hash {
		return ErrBadServerSecret
	}
	if _, err := tx.ExecContext(ctx, `UPDATE server_claims SET retry_key_hash = NULL WHERE used_server_id = $1 AND retry_key_hash IS NOT NULL`, serverID); err != nil {
		return err
	}
	return tx.Commit()
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
