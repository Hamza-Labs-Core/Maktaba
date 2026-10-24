package cluster

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
)

// ScramVerifier is Postgres's SCRAM-SHA-256 verifier of password (RFC 5802 / 7677: a
// random salt, 4096 iterations), as Postgres stores it for a role: a role made or changed
// with it checks the password at sign-in, and no statement, log or pg_stat_activity ever
// carries the password itself (nodecopy.scramVerifier makes the copies' the same way).
func ScramVerifier(password string) (string, error) {
	const iterations = 4096
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}
	mac := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac(salted, "Client Key"))
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(stored[:]),
		b64(mac(salted, "Server Key"))), nil
}

// Term is the cluster's current term (0 before its first, or on a database without the
// terms: a home from before Epic 37).
func (p *PGRoles) Term(ctx context.Context) int64 {
	var t sql.NullInt64
	if err := p.DB.QueryRowContext(ctx, `SELECT max(term) FROM cluster_terms`).Scan(&t); err != nil {
		return 0
	}
	return t.Int64
}
