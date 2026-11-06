package log

import (
	"log/slog"
	"strings"
)

// DefaultRedactedFields is the list of attribute keys whose values are masked
// whole before emission: the shared list's "keys" (shared/redact/rules.json,
// Epic 53 story 53.01). The match is case-insensitive. A key ending in one of
// the list's suffixes (cf_api_token, smtp_password) is masked too, and a string
// value whose key merely contains one of its parts (plex_token, database_url).
//
// Services can extend it via Options.RedactedFields.
var DefaultRedactedFields = append([]string(nil), sharedRules.Keys...)

func keySensitive(redact map[string]struct{}, key string, isString bool) bool {
	if _, ok := redact[strings.ToLower(key)]; ok {
		return true
	}
	return sharedRules.KeySensitive(key, isString)
}

// RedactURL masks the credentials a URL (or any text holding one, such as a
// *url.Error's message) carries in its query string or userinfo. Path segments
// stay.
func RedactURL(s string) string {
	if !strings.Contains(s, "=") && !strings.Contains(s, "@") {
		return s
	}
	return Scrub(s)
}

// Scrub applies the shared value rules (shared/redact/rules.json) to text: JWTs,
// bearer tokens, URL passwords, query credentials, emails, home directories
// (/Users/<name>/ → ~/), card numbers and the host part of IP addresses.
func Scrub(s string) string { return sharedRules.Scrub(s) }

// redactValue scrubs a string or error attribute; ok is false when nothing in it
// changed.
func redactValue(v slog.Value) (string, bool) {
	var s string
	switch v.Kind() {
	case slog.KindString:
		s = v.String()
	case slog.KindAny:
		err, isErr := v.Any().(error)
		if !isErr || err == nil {
			return "", false
		}
		s = err.Error()
	default:
		return "", false
	}
	if r := Scrub(s); r != s {
		return r, true
	}
	return "", false
}
