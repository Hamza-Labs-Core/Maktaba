// Package httperror is the only place in the API that emits an
// `application/problem+json` response (RFC 9457). Every handler returns
// errors of type *Error or types that wrap one; Write renders them.
//
// Type URIs are namespaced under https://maktaba.dev/problems/ so that a
// future spec page can document each one with a stable identifier. They
// are not URLs that need to resolve — RFC 9457 §3.1 explicitly allows
// them to be opaque strings — but we keep the URI form in case we ever
// publish that page.
package httperror

// Problem-type URIs. Each later story adds its own constants here so
// that the registry has a single source of truth.
const (
	TypeBadRequest           = "https://maktaba.dev/problems/bad-request"
	TypeInvalidJSON          = "https://maktaba.dev/problems/invalid-json"
	TypeInvalidQueryParam    = "https://maktaba.dev/problems/invalid-query-parameter"
	TypeInvalidCursor        = "https://maktaba.dev/problems/invalid-cursor"
	TypeCursorUnsupported    = "https://maktaba.dev/problems/cursor-unsupported-version"
	TypeNotFound             = "https://maktaba.dev/problems/not-found"
	TypeValidation           = "https://maktaba.dev/problems/validation"
	TypeIdempotencyConflict  = "https://maktaba.dev/problems/idempotency-key-conflict"
	TypeConfirmationReq      = "https://maktaba.dev/problems/confirmation-required"
	TypeInternal             = "https://maktaba.dev/problems/internal"
	TypeUnavailable          = "https://maktaba.dev/problems/unavailable"
	TypeForbidden            = "https://maktaba.dev/problems/forbidden"
	TypeConflict             = "https://maktaba.dev/problems/conflict"
	TypeBodyTooLarge         = "https://maktaba.dev/problems/body-too-large"
	TypeRateLimited          = "https://maktaba.dev/problems/rate-limited"
	TypeUnsupportedMediaType = "https://maktaba.dev/problems/unsupported-media-type"
	TypeMethodNotAllowed     = "https://maktaba.dev/problems/method-not-allowed"
)
