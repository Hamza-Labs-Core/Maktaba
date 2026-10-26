// Package password centralises the password-strength policy used at
// registration and password-change. Cloud applies a slightly stricter
// rule than the on-prem api because the cloud surface is internet-
// facing: 10-char minimum, NIST 800-63B style — no per-character
// composition rules, no rotation, but we DO check against a small
// embedded leak-prefix list so the most-common breached passwords are
// refused.
package password

import (
	"errors"
	"strings"
)

const (
	MinLen = 10
	MaxLen = 256
)

var (
	ErrTooShort   = errors.New("password: minimum length is 10")
	ErrTooLong    = errors.New("password: exceeds 256 chars")
	ErrLeaked     = errors.New("password: appears in breach corpus")
	ErrWhitespace = errors.New("password: cannot start or end with whitespace")
)

// CommonLeakedPasswords is a hand-picked subset of the top-100 most
// common passwords from public breach corpora that are at least MinLen
// chars (shorter ones get rejected on length anyway). Prod additionally
// queries the k-anon HIBP API behind a feature flag; this local set is
// the offline baseline so unit tests cover the policy.
var CommonLeakedPasswords = []string{
	"password123",
	"password1234",
	"letmein123",
	"qwertyuiop",
	"qwerty12345",
	"iloveyou123",
	"welcome123",
	"admin1234",
	"administrator",
	"changeme123",
}

// commonLeaked is the list as a set. The comparison is on the
// plaintext in memory; nothing here is stored, so there is no reason to
// hash it.
var commonLeaked = func() map[string]bool {
	m := make(map[string]bool, len(CommonLeakedPasswords))
	for _, p := range CommonLeakedPasswords {
		m[p] = true
	}
	return m
}()

// Validate runs the policy checks. Callers do this BEFORE hashing.
func Validate(p string) error {
	if len(p) < MinLen {
		return ErrTooShort
	}
	if len(p) > MaxLen {
		return ErrTooLong
	}
	if p != strings.TrimSpace(p) {
		return ErrWhitespace
	}
	if commonLeaked[p] {
		return ErrLeaked
	}
	return nil
}
