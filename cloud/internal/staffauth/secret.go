package staffauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
)

// Box seals a TOTP secret at rest with AES-256-GCM under MAKTABA_CLOUD_STAFF_SECRET_KEY
// (story 50.33 §1): a copy of the database alone can't mint codes.
type Box struct{ aead cipher.AEAD }

// ParseKey reads MAKTABA_CLOUD_STAFF_SECRET_KEY: 32 bytes as 64 hex digits or as base64
// (`openssl rand -hex 32` or `openssl rand -base64 32`).
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("staffauth: the secret key is not set")
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("staffauth: the secret key must be 32 bytes, as 64 hex digits or base64")
}

// NewBox takes the 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("staffauth: the secret key is %d bytes, want 32", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain, bound to what (the staff id: a sealed secret moved to another row
// doesn't open).
func (b *Box) Seal(plain, what string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), []byte(what))), nil
}

// Open decrypts what Seal wrote for the same what.
func (b *Box) Open(sealed, what string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("staffauth: sealed secret malformed")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], []byte(what))
	if err != nil {
		return "", errors.New("staffauth: sealed secret does not open")
	}
	return string(plain), nil
}

// RecoveryCodeCount is how many recovery codes enrolment hands out (§1).
const RecoveryCodeCount = 10

// NewRecoveryCodes are RecoveryCodeCount fresh codes, xxxxx-xxxxx in lowercase base32
// (50 bits each), and their argon2id hashes for the database.
func NewRecoveryCodes() (codes, hashes []string, err error) {
	for range RecoveryCodeCount {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		s := strings.ToLower(b32.EncodeToString(b))[:10]
		code := s[:5] + "-" + s[5:]
		h, err := argon2id.Hash(NormalizeRecoveryCode(code), argon2id.DefaultParams())
		if err != nil {
			return nil, nil, err
		}
		codes, hashes = append(codes, code), append(hashes, h)
	}
	return codes, hashes, nil
}

// NormalizeRecoveryCode drops what a person adds or changes when typing one: spaces, the
// dash, case.
func NormalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// TokenHash is how a raw cookie, enrolment or mfa token is kept: SHA-256, hex.
func TokenHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// NewRawToken is 32 random bytes, base64url: a cookie, an enrolment link or an mfa_token.
func NewRawToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
