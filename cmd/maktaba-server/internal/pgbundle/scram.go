package pgbundle

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// scramIterations is Postgres's own default (scram_iterations).
const scramIterations = 4096

// scramVerifier is the SCRAM-SHA-256 secret Postgres stores for password (RFC 5802,
// RFC 7677; Postgres's scram_build_secret): ALTER ROLE … PASSWORD '<it>' sets the
// password without the statement carrying it, so the statement Postgres echoes on an
// error (its log's STATEMENT line, psql's) names no password. The password is
// ensurePassword's (base64url), which SASLprep leaves as it is.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return scramVerifierWith(password, salt, scramIterations)
}

func scramVerifierWith(password string, salt []byte, iter int) (string, error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iter, sha256.Size)
	if err != nil {
		return "", err
	}
	mac := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac(salted, "Client Key"))
	server := mac(salted, "Server Key")
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iter, b64(salt), b64(stored[:]), b64(server)), nil
}
