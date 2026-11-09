// Package staffauth is the operator console's own sign-in (Epic 50 story 50.33): staff
// accounts apart from customer accounts, a password plus a second factor that can't be
// skipped, short sessions, re-authentication for the actions that hurt, and lockout.
package staffauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238's default, what every authenticator app speaks
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP is RFC 6238 as story 50.33 §2 fixes it: SHA-1, 6 digits, 30 s steps, one step of
// drift either way.
const (
	totpDigits = 6
	totpPeriod = 30
	totpDrift  = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret is a fresh 20-byte secret (RFC 4226's recommended length), base32 for the
// authenticator app.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// TOTPStep is the 30 s step t falls in.
func TOTPStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// totpAt is the code for step.
func totpAt(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // steps are positive
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// TOTPCode is the current code for a base32 secret (enrolment's check, and the tests).
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("staffauth: totp secret: %w", err)
	}
	return totpAt(key, TOTPStep(t)), nil
}

// VerifyTOTP checks code against secret at now, within one step either way, and returns the
// step it matched. A step at or before lastStep is refused: a code is good once, so one seen
// over a shoulder or replayed from a log can't be used again inside its window.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil || len(code) != totpDigits {
		return 0, false
	}
	cur := TOTPStep(now)
	for d := int64(-totpDrift); d <= totpDrift; d++ {
		step := cur + d
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpAt(key, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// TOTPURI is the otpauth:// link the enrolment page draws as a QR code.
func TOTPURI(secret, email string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "Maktaba console")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape("Maktaba console:"+email) + "?" + q.Encode()
}
