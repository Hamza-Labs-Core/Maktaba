package log

import (
	"log/slog"
	"regexp"
	"strings"
)

// DefaultRedactedFields is the seed list of attribute keys whose values
// are masked before emission. The match is case-insensitive (handler
// lower-cases the incoming key before lookup).
//
// The list is intentionally short and additive — Story 21.8 (privacy)
// wires a richer redaction layer over the same hook, and individual
// services can extend it via Options.RedactedFields.
//
// Anything that resembles a credential, secret, or full HTTP body
// belongs here. Resource ids (request_id, video_id) do not — they are
// safe to log and are part of the contextual fields.
var DefaultRedactedFields = []string{
	"password",
	"passwd",
	"pwd",
	"secret",
	"token",
	"access_token",
	"refresh_token",
	"id_token",
	"api_key",
	"apikey",
	"authorization",
	"auth",
	"cookie",
	"set_cookie",
	"session_cookie",
	"private_key",
	"client_secret",
	"credit_card",
	"card_number",
	"cvv",
	"ssn",
}

// sensitiveSuffixes extend the exact list: a key that ends in one of these
// (cf_api_token, jwt_private_key, smtp_password) is masked too.
var sensitiveSuffixes = []string{
	"_password", "_passwd", "_secret", "_token", "_api_key", "_apikey",
	"_private_key", "_privkey",
}

func keySensitive(redact map[string]struct{}, key string) bool {
	low := strings.ToLower(key)
	if _, ok := redact[low]; ok {
		return true
	}
	for _, s := range sensitiveSuffixes {
		if strings.HasSuffix(low, s) {
			return true
		}
	}
	return false
}

// secretQuery matches a credential carried in a URL's query string
// (?sig=, &token=, ?X-Plex-Token=, &api_key=, ...) up to the next separator.
var secretQuery = regexp.MustCompile(
	`(?i)([?&](?:sig|signature|token|access_token|refresh_token|key|api_key|apikey|password|x-plex-token)=)[^&\s#"']+`)

// RedactURL masks the credentials a URL (or any text holding one, such as a
// *url.Error's message) carries in its query string. Path segments stay.
func RedactURL(s string) string {
	if !strings.Contains(s, "=") {
		return s
	}
	return secretQuery.ReplaceAllString(s, "${1}"+redactedValue)
}

// redactValue masks the URL credentials in a string or error attribute
// (a url field, a *url.Error in err); ok is false when there were none.
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
	if !strings.Contains(s, "=") {
		return "", false
	}
	if r := RedactURL(s); r != s {
		return r, true
	}
	return "", false
}
