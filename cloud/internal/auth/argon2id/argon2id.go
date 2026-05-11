// Package argon2id hashes passwords for the cloud identity store.
//
// It mirrors the api/internal/auth/argon2id package by design — the
// cloud and on-prem services use the same algorithm and PHC-string
// format so an account migrated from one to the other never needs a
// password reset. Anywhere this file diverges from the api copy is a
// bug.
package argon2id

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params controls argon2id cost. Defaults track OWASP's 2024 baseline
// for an interactive login flow (64 MiB / 2 iterations / 1 lane).
type Params struct {
	Memory      uint32
	Time        uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
	MaxLen      int
}

func DefaultParams() Params {
	return Params{
		Memory:      64 * 1024,
		Time:        2,
		Parallelism: 1,
		SaltLen:     16,
		KeyLen:      32,
		MaxLen:      256,
	}
}

var (
	ErrMismatch   = errors.New("argon2id: password mismatch")
	ErrTooLong    = errors.New("argon2id: password exceeds max length")
	ErrBadFormat  = errors.New("argon2id: not a PHC argon2id string")
	ErrUnsupportedVer = errors.New("argon2id: unsupported version")
)

// Hash derives a PHC-format encoded hash. Includes parameters so Verify
// can read them back even after we tune the defaults later.
func Hash(password string, p Params) (string, error) {
	if len(password) > p.MaxLen {
		return "", ErrTooLong
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.Memory, p.Time, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify constant-time-compares password against the PHC string.
func Verify(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrBadFormat
	}
	var ver int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &ver); err != nil {
		return ErrBadFormat
	}
	if ver != argon2.Version {
		return ErrUnsupportedVer
	}
	var mem, t uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &par); err != nil {
		return ErrBadFormat
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrBadFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrBadFormat
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, par, uint32(len(want)))
	if subtle.ConstantTimeCompare(want, got) != 1 {
		return ErrMismatch
	}
	return nil
}
