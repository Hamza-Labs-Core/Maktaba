package license

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Errors a home's license request can end in.
var (
	// ErrNoLicense: the owner's plan grants none (the free plan).
	ErrNoLicense = errors.New("license: the plan grants no license")
	// ErrRevoked: the license id is revoked, or the owner's account is
	// suspended (a disputed payment): the home locks at once.
	ErrRevoked = errors.New("license: revoked")
	// ErrUnknownServer: no such home.
	ErrUnknownServer = errors.New("license: unknown server")
)

// Issuer signs licenses with the cloud's entitlement key and keeps the
// record (slot 0014).
type Issuer struct {
	DB   *sql.DB
	Priv ed25519.PrivateKey
	Now  func() time.Time
}

func (is *Issuer) now() time.Time {
	if is.Now != nil {
		return is.Now()
	}
	return time.Now()
}

// ForServer is the license the owner of serverID's plan grants it,
// recorded as issued.
func (is *Issuer) ForServer(ctx context.Context, serverID string) (License, error) {
	var plan, status, email string
	var userID string
	err := is.DB.QueryRowContext(ctx, `
        SELECT u.id::text, u.plan, u.status, u.email
          FROM servers s JOIN users u ON u.id = s.owner_user_id
         WHERE s.id::text = $1`, serverID).Scan(&userID, &plan, &status, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return License{}, ErrUnknownServer
	}
	if err != nil {
		return License{}, err
	}
	id := PlanLicenseID(serverID)
	if status != "active" {
		return License{}, ErrRevoked
	}
	if revoked, err := is.IsRevoked(ctx, id); err != nil {
		return License{}, err
	} else if revoked {
		return License{}, ErrRevoked
	}
	tier := TierForPlan(plan)
	if tier == "" {
		return License{}, ErrNoLicense
	}
	lic, err := ForPlan(is.Priv, serverID, tier, email, is.now())
	if err != nil {
		return License{}, err
	}
	return lic, is.record(ctx, "plan", userID, serverID, lic.License)
}

// Offline signs a license for any home (the operator's CLI): no server
// binding, valid until it expires or is revoked.
func (is *Issuer) Offline(ctx context.Context, tier string, seats int, holder string, valid time.Duration) (License, error) {
	if tier != TierHome && tier != TierPro {
		return License{}, fmt.Errorf("license: tier must be %q or %q", TierHome, TierPro)
	}
	if seats < 0 {
		return License{}, errors.New("license: seats must be >= 0 (0: the tier's cap)")
	}
	if valid <= 0 {
		return License{}, errors.New("license: validity must be positive")
	}
	id, err := newID()
	if err != nil {
		return License{}, err
	}
	now := is.now().UTC().Truncate(time.Second)
	lic, err := Sign(is.Priv, LicenseInner{
		LicenseID: id, Tier: tier, Seats: seats, IssuedAt: now, ExpiresAt: now.Add(valid), Holder: holder,
	})
	if err != nil {
		return License{}, err
	}
	if is.DB != nil {
		if err := is.record(ctx, "offline", "", "", lic.License); err != nil {
			return License{}, err
		}
	}
	return lic, nil
}

func (is *Issuer) record(ctx context.Context, kind, userID, serverID string, l LicenseInner) error {
	_, err := is.DB.ExecContext(ctx, `
        INSERT INTO licenses_issued (license_id, kind, user_id, server_id, tier, seats, holder, issued_at, expires_at)
        VALUES ($1, $2, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid, $5, $6, $7, $8, $9)
        ON CONFLICT (license_id) DO UPDATE SET
            tier = EXCLUDED.tier, seats = EXCLUDED.seats, holder = EXCLUDED.holder,
            user_id = EXCLUDED.user_id, issued_at = EXCLUDED.issued_at,
            expires_at = EXCLUDED.expires_at, updated_at = now()`,
		l.LicenseID, kind, userID, serverID, l.Tier, l.Seats, l.Holder, l.IssuedAt, l.ExpiresAt)
	return err
}

// IsRevoked reports whether id is on the revocation list.
func (is *Issuer) IsRevoked(ctx context.Context, id string) (bool, error) {
	var one int
	err := is.DB.QueryRowContext(ctx, `SELECT 1 FROM license_revocations WHERE license_id = $1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Revocations is the signed list of revoked license ids.
func (is *Issuer) Revocations(ctx context.Context) (RevocationList, error) {
	rows, err := is.DB.QueryContext(ctx, `SELECT license_id FROM license_revocations ORDER BY license_id`)
	if err != nil {
		return RevocationList{}, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return RevocationList{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return RevocationList{}, err
	}
	return SignRevocations(is.Priv, RevocationsInner{IssuedAt: is.now().UTC().Truncate(time.Second), LicenseIDs: ids})
}

// Revoke puts id on the list (idempotent).
func (is *Issuer) Revoke(ctx context.Context, id, reason, by string) error {
	_, err := is.DB.ExecContext(ctx, `
        INSERT INTO license_revocations (license_id, reason, revoked_by) VALUES ($1, $2, $3)
        ON CONFLICT (license_id) DO NOTHING`, id, reason, by)
	return err
}

// Unrevoke takes id off the list; false when it was not on it.
func (is *Issuer) Unrevoke(ctx context.Context, id string) (bool, error) {
	res, err := is.DB.ExecContext(ctx, `DELETE FROM license_revocations WHERE license_id = $1`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Issued is one row of the operator's license list.
type Issued struct {
	LicenseID string     `json:"license_id"`
	Kind      string     `json:"kind"`
	UserID    *string    `json:"user_id,omitempty"`
	ServerID  *string    `json:"server_id,omitempty"`
	Tier      string     `json:"tier"`
	Seats     int        `json:"seats"`
	Holder    string     `json:"holder"`
	IssuedAt  time.Time  `json:"issued_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Reason    *string    `json:"revoked_reason,omitempty"`
}

// List returns the issued licenses, newest first.
func (is *Issuer) List(ctx context.Context, limit, offset int) ([]Issued, int, error) {
	var total int
	if err := is.DB.QueryRowContext(ctx, `SELECT count(*) FROM licenses_issued`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := is.DB.QueryContext(ctx, `
        SELECT i.license_id, i.kind, i.user_id::text, i.server_id::text, i.tier, i.seats, i.holder,
               i.issued_at, i.expires_at, r.revoked_at, r.reason
          FROM licenses_issued i LEFT JOIN license_revocations r ON r.license_id = i.license_id
         ORDER BY i.updated_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Issued{}
	for rows.Next() {
		var x Issued
		var rev sql.NullTime
		if err := rows.Scan(&x.LicenseID, &x.Kind, &x.UserID, &x.ServerID, &x.Tier, &x.Seats, &x.Holder,
			&x.IssuedAt, &x.ExpiresAt, &rev, &x.Reason); err != nil {
			return nil, 0, err
		}
		if rev.Valid {
			x.RevokedAt = &rev.Time
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}
