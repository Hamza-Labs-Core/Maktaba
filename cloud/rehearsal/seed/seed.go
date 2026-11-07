// Package seed fills a rehearsal's cloud database with simulated homes
// (Epic 50 story 50.06): one owner, and a servers row per simhome with its
// slug and its secret hashed with cheap argon2id parameters (the hash
// carries them, so the relay's check is the real one; 1,000 homes connecting
// at once would otherwise need 64 GB for the production parameters).
package seed

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
)

// cheap is argon2id at 1 MiB, one pass: rehearsal homes only.
var cheap = func() argon2id.Params {
	p := argon2id.DefaultParams()
	p.Memory, p.Time = 1024, 1
	return p
}()

// Homes makes simulated homes first..first+n-1 (simhome.Creds), on plan, and
// returns their server ids by slug. Rows that exist are kept.
func Homes(ctx context.Context, db *sql.DB, first, n int, plan string) (map[string]string, error) {
	var owner string
	err := db.QueryRowContext(ctx, `
        INSERT INTO users (email, password_hash, display_name, email_verified)
        VALUES ('rehearsal@example.test', '', 'Rehearsal', true)
        ON CONFLICT (email) DO UPDATE SET display_name = EXCLUDED.display_name
        RETURNING id`).Scan(&owner)
	if err != nil {
		return nil, fmt.Errorf("seed: owner: %w", err)
	}
	ids := make(map[string]string, n)
	for i := first; i < first+n; i++ {
		_, secret, slug := simhome.Creds(i)
		hash, err := argon2id.Hash(secret, cheap)
		if err != nil {
			return nil, err
		}
		var id string
		err = db.QueryRowContext(ctx, `
            INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan)
            VALUES ($1, $2, $2, $3, $4)
            ON CONFLICT (slug) DO UPDATE SET server_secret_hash = EXCLUDED.server_secret_hash, plan = EXCLUDED.plan
            RETURNING id`, owner, slug, hash, plan).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("seed: %s: %w", slug, err)
		}
		ids[slug] = id
	}
	return ids, nil
}
