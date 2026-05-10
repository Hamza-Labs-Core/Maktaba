// Package argon2id implements password hashing for Story 10.1.
//
// All hashes are emitted in PHC-format (`$argon2id$v=19$m=…,t=…,p=…$salt$hash`)
// so tuning the parameters in config does NOT invalidate existing
// rows — every hash carries the parameters it was created with, and
// Verify reads them back from the stored string (AC-1).
//
// We deliberately keep the API surface tiny: one Hash, one Verify,
// one Params. Higher layers (the users store, the login handler) deal
// with usernames, lockouts, and rate-limiting.
package argon2id

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params is the tunable side of argon2id. Defaults match Story 10.1
// AC-1 (and architecture §11.2):
//
//	memory      = 65536 KiB
//	time        = 2
//	parallelism = 1
//	saltLen     = 16 bytes
//	keyLen      = 32 bytes
//	maxLen      = 256 bytes (refuse passwords longer than this; AC test)
type Params struct {
	Memory      uint32
	Time        uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
	MaxLen      int
}

// DefaultParams returns the production defaults defined in Story 10.1.
// Keep this function pure: callers may take it as a base and tweak
// fields, e.g. config-driven memory bumps.
func DefaultParams() Params {
	return Params{
		Memory:      64 * 1024, // 64 MiB = 65536 KiB
		Time:        2,
		Parallelism: 1,
		SaltLen:     16,
		KeyLen:      32,
		MaxLen:      256,
	}
}

// ErrPasswordTooLong is returned by Hash when the input exceeds
// Params.MaxLen. The cap protects the argon2id worker from a
// trivial DoS where a 1 MB password takes seconds to hash.
var ErrPasswordTooLong = errors.New("password exceeds maximum length")

// ErrInvalidHash is returned when a stored PHC string can't be parsed.
// We never return this through to the user; the login handler maps it
// to a generic "invalid credentials" so an attacker can't distinguish
// a corrupted DB row from a wrong password.
var ErrInvalidHash = errors.New("invalid argon2id hash format")

// ErrIncompatibleVersion is returned when a stored hash uses an
// argon2 version newer than the binary's library can verify.
var ErrIncompatibleVersion = errors.New("incompatible argon2 version")

// ErrMismatch is returned by Verify when the password doesn't match.
// This is the only error a login handler should map to "401 invalid
// credentials"; the others indicate a configuration or DB bug.
var ErrMismatch = errors.New("password mismatch")

// Hash returns the PHC-format string for `password` under `p`. The
// argon2 derivation is constant-time relative to the parameters, but
// allocates `p.Memory` KiB; callers should pool concurrency.
func Hash(password string, p Params) (string, error) {
	if p.MaxLen > 0 && len(password) > p.MaxLen {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.Memory, p.Time, p.Parallelism,
		b64.EncodeToString(salt),
		b64.EncodeToString(key),
	), nil
}

// Verify checks `password` against `phc`. Returns nil when the
// password matches; ErrMismatch when it doesn't; ErrInvalidHash /
// ErrIncompatibleVersion when the stored hash is broken.
//
// The stored hash carries its own parameters so a stronger global
// config doesn't invalidate older rows (AC-1).
func Verify(password, phc string) error {
	parts := strings.Split(phc, "$")
	// Expected: ["", "argon2id", "v=19", "m=…,t=…,p=…", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return ErrInvalidHash
	}
	if version != argon2.Version {
		return ErrIncompatibleVersion
	}
	var memory uint32
	var timeCost uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &parallelism); err != nil {
		return ErrInvalidHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return ErrInvalidHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(want, got) == 1 {
		return nil
	}
	return ErrMismatch
}

// IsDisabled reports whether the stored hash is the
// `<unsalted-disabled>` placeholder we put on the sentinel admin row
// in migration 0029. A row with this value is meant to be reachable
// only via the single-user admin-token bypass (Story 10.9), never via
// password login. Callers should refuse password login if true.
func IsDisabled(phc string) bool {
	return phc == "<unsalted-disabled>" || !bytes.HasPrefix([]byte(phc), []byte("$argon2id$"))
}
