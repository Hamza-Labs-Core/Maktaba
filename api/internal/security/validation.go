// Package security holds the cross-cutting security primitives wired
// across Epic 23 stories:
//
//   - Validation: type-safe input validators (UUID, library path, search
//     query, language tag, pagination bounds).
//   - RateLimiter: a per-key token bucket used by the per-IP / per-user
//     middleware and by any handler that wants additional limiting.
//   - SBOM: a small parser that surfaces the build-time SBOM at
//     /api/system/sbom.
//   - DisclosurePolicy: the disclosure metadata served at /.well-known/security.txt.
//
// The package is HTTP-agnostic; the security HTTP surface lives in
// api/internal/handlers/security.
package security

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ErrValidation is the sentinel returned by validators.
var ErrValidation = errors.New("validation failed")

// ValidationError is the field-level detail returned to callers.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements error so handlers can wrap it.
func (v ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", v.Field, v.Message)
}

// uuidRE matches the canonical 8-4-4-4-12 form. We intentionally accept
// only lowercase to make audit-log queries deterministic.
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidateUUID returns a ValidationError if s is not a lowercase UUID.
func ValidateUUID(field, s string) error {
	if !uuidRE.MatchString(s) {
		return ValidationError{Field: field, Message: "must be a lowercase UUID"}
	}
	return nil
}

// langRE matches BCP-47 language tags up to 3 components: en, en-US, en-Latn-US.
var langRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z][a-z]{3})?(-[A-Z]{2})?$`)

// ValidateLangTag returns an error if s isn't a recognised tag.
func ValidateLangTag(field, s string) error {
	if !langRE.MatchString(s) {
		return ValidationError{Field: field, Message: "must be a BCP-47 language tag (e.g. en, en-US)"}
	}
	return nil
}

// ValidateSearchQuery enforces the documented bounds: 1–256 UTF-8 chars,
// no NUL bytes. We do not strip operators here — the FTS engine owns that.
func ValidateSearchQuery(field, s string) error {
	if s == "" {
		return ValidationError{Field: field, Message: "must not be empty"}
	}
	if !utf8.ValidString(s) {
		return ValidationError{Field: field, Message: "must be valid UTF-8"}
	}
	if strings.ContainsRune(s, 0) {
		return ValidationError{Field: field, Message: "must not contain NUL"}
	}
	if utf8.RuneCountInString(s) > 256 {
		return ValidationError{Field: field, Message: "must be ≤ 256 characters"}
	}
	return nil
}

// ValidateLibraryPath rejects paths with `..`, embedded NULs, or absolute
// paths outside the library root. We don't check filesystem existence
// here — the caller already does that.
func ValidateLibraryPath(field, s string) error {
	if s == "" {
		return ValidationError{Field: field, Message: "must not be empty"}
	}
	if strings.ContainsRune(s, 0) {
		return ValidationError{Field: field, Message: "must not contain NUL"}
	}
	if strings.Contains(s, "..") {
		return ValidationError{Field: field, Message: "must not contain '..'"}
	}
	if strings.HasPrefix(s, "/") {
		return ValidationError{Field: field, Message: "must be relative to library root"}
	}
	return nil
}

// ValidateEmail wraps net/mail.ParseAddress with a friendlier message.
func ValidateEmail(field, s string) error {
	if _, err := mail.ParseAddress(s); err != nil {
		return ValidationError{Field: field, Message: "must be a valid email address"}
	}
	return nil
}

// ValidatePaginationCursor accepts only base64-ish opaque tokens up to
// 256 bytes. Empty string is treated as "no cursor".
func ValidatePaginationCursor(field, s string) error {
	if s == "" {
		return nil
	}
	if len(s) > 256 {
		return ValidationError{Field: field, Message: "must be ≤ 256 bytes"}
	}
	for _, r := range s {
		if !(('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') ||
			('0' <= r && r <= '9') || r == '-' || r == '_' || r == '=' || r == '.') {
			return ValidationError{Field: field, Message: "must be base64url"}
		}
	}
	return nil
}

// FirstError returns the first non-nil error from the list, or nil.
// Handlers compose validators by passing each to this.
func FirstError(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
