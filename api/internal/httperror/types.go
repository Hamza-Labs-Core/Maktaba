// Package httperror is the only place in the API that emits an
// `application/problem+json` response (RFC 9457). Every handler returns
// errors of type *Error or types that wrap one; Write renders them.
//
// Type URIs are namespaced under https://maktaba.tv/problems/ so that a
// future spec page can document each one with a stable identifier. They
// are not URLs that need to resolve — RFC 9457 §3.1 explicitly allows
// them to be opaque strings — but we keep the URI form in case we ever
// publish that page.
package httperror

// Problem-type URIs. Each later story adds its own constants here so
// that the registry has a single source of truth.
const (
	TypeBadRequest           = "https://maktaba.tv/problems/bad-request"
	TypeInvalidJSON          = "https://maktaba.tv/problems/invalid-json"
	TypeInvalidQueryParam    = "https://maktaba.tv/problems/invalid-query-parameter"
	TypeInvalidCursor        = "https://maktaba.tv/problems/invalid-cursor"
	TypeCursorUnsupported    = "https://maktaba.tv/problems/cursor-unsupported-version"
	TypeNotFound             = "https://maktaba.tv/problems/not-found"
	TypeValidation           = "https://maktaba.tv/problems/validation"
	TypeIdempotencyConflict  = "https://maktaba.tv/problems/idempotency-key-conflict"
	TypeConfirmationReq      = "https://maktaba.tv/problems/confirmation-required"
	TypeInternal             = "https://maktaba.tv/problems/internal"
	TypeUnavailable          = "https://maktaba.tv/problems/unavailable"
	TypeBadGateway           = "https://maktaba.tv/problems/bad-gateway"
	TypeForbidden            = "https://maktaba.tv/problems/forbidden"
	TypeConflict             = "https://maktaba.tv/problems/conflict"
	TypeBodyTooLarge         = "https://maktaba.tv/problems/body-too-large"
	TypeRateLimited          = "https://maktaba.tv/problems/rate-limited"
	TypeUnsupportedMediaType = "https://maktaba.tv/problems/unsupported-media-type"
	TypeMethodNotAllowed     = "https://maktaba.tv/problems/method-not-allowed"
)
