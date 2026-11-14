package staffauth

// Sensitive are the admin routes that need an MFA check within the last five minutes (story
// 50.33 §5), as "METHOD /route/pattern" exactly as chi registers them. 50.24's actions join
// this list when they land.
var Sensitive = map[string]bool{
	"POST /v1/admin/accounts/{id}/suspend":   true,
	"POST /v1/admin/accounts/{id}/unsuspend": true,
	"POST /v1/admin/accounts/{id}/plan":      true,
	"POST /v1/admin/servers/{id}/broadcast":  true,
	"PUT /v1/admin/broadcast/geo-deny":       true,
	"POST /v1/admin/abuse/block":             true,
	"POST /v1/admin/abuse/unblock":           true,
	"POST /v1/admin/licenses/{id}/revoke":    true,
	"POST /v1/admin/licenses/{id}/unrevoke":  true,
	"DELETE /v1/admin/privacy/users/{id}":    true,
	// the broadcast kill switch (50.25): taking channels off air, and lifting it
	"POST /v1/admin/broadcast/revocations":           true,
	"POST /v1/admin/broadcast/revocations/{id}/lift": true,
}

// NotSensitive are the mutating admin routes that need no re-authentication, each with its
// reason. A mutating route in neither list fails the test that walks the admin router (T08).
var NotSensitive = map[string]string{
	"POST /v1/admin/reports/{id}/status": "triage of a home's problem report: changes no account, license, block or data",
	"POST /v1/admin/reports/{id}/issue":  "links a problem report to an issue: triage only",
	"POST /v1/admin/reports/{id}/reply":  "a reply shown to the home that sent the report: triage only",
}

// IsSensitive reports whether route (chi's pattern) with method needs a fresh MFA check.
func IsSensitive(method, route string) bool { return Sensitive[method+" "+route] }
