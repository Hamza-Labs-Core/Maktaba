package log

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
