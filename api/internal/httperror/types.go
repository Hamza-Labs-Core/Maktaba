// Package httperror is the only place in the API that emits an
// `application/problem+json` response (RFC 9457). Every handler returns
// errors of type *Error or types that wrap one; Write renders them.
//
// Type URIs are namespaced under https://maktaba.tv/problems/, and each one
// is an entry of the problem-type registry (shared/api/problems.json, Epic
// 60 story 60.06): maktaba.tv publishes a page per entry, saying what it
// means and what a person can do. Write refuses a type the registry lacks.
package httperror

// The generic problem types, and those several packages send. A package's
// own types sit beside its handlers, each made by Type.
var (
	TypeBadRequest           = Type("bad-request")
	TypeInvalidJSON          = Type("invalid-json")
	TypeInvalidQueryParam    = Type("invalid-query-parameter")
	TypeInvalidCursor        = Type("invalid-cursor")
	TypeCursorUnsupported    = Type("cursor-unsupported-version")
	TypeNotFound             = Type("not-found")
	TypeValidation           = Type("validation")
	TypeIdempotencyConflict  = Type("idempotency-key-conflict")
	TypeConfirmationReq      = Type("confirmation-required")
	TypeInternal             = Type("internal")
	TypeUnavailable          = Type("unavailable")
	TypeBadGateway           = Type("bad-gateway")
	TypeForbidden            = Type("forbidden")
	TypeConflict             = Type("conflict")
	TypeBodyTooLarge         = Type("body-too-large")
	TypeRateLimited          = Type("rate-limited")
	TypeUnsupportedMediaType = Type("unsupported-media-type")
	TypeMethodNotAllowed     = Type("method-not-allowed")
	TypeUnauthorized         = Type("unauthorized")
	TypeAuthRequired         = Type("auth-required")
	TypeCSRFMismatch         = Type("csrf-mismatch")
	TypeInvalidCredentials   = Type("invalid-credentials")
	TypeAccountLocked        = Type("account-locked")
	TypeCloudSession         = Type("cloud-session")
	TypeHomeUnreachable      = Type("home-unreachable")
	TypeEmailExists          = Type("email-exists")
	TypeUsernameExists       = Type("username-exists")
)
