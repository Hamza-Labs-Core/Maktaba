package staffauth

// Sensitive are the admin routes that need an MFA check within the last five minutes (story
// 50.33 §5), as "METHOD /route/pattern" exactly as chi registers them.
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
	// takedowns and what undoes them (50.24): taking an item, a channel or a publisher down,
	// putting it back, withdrawing a notice or a strike, reinstating a publisher, suspending a
	// notifier from the form
	"POST /v1/admin/moderation/actions":                   true,
	"POST /v1/admin/moderation/actions/{id}/restore":      true,
	"POST /v1/admin/moderation/notices/{id}/withdraw":     true,
	"POST /v1/admin/moderation/strikes/{id}/withdraw":     true,
	"POST /v1/admin/moderation/publishers/{id}/reinstate": true,
	"POST /v1/admin/moderation/notifiers/suspend":         true,
}

// NotSensitive are the mutating admin routes that need no re-authentication, each with its
// reason. A mutating route in neither list fails the test that walks the admin router (T08).
var NotSensitive = map[string]string{
	"POST /v1/admin/reports/{id}/status": "triage of a home's problem report: changes no account, license, block or data",
	"POST /v1/admin/reports/{id}/issue":  "links a problem report to an issue: triage only",
	"POST /v1/admin/reports/{id}/reply":  "a reply shown to the home that sent the report: triage only",
	// the moderation queue's triage (50.24): each carries its reason and is audited
	"POST /v1/admin/moderation/notices":                      "enters an emailed notice into the queue: no action",
	"POST /v1/admin/moderation/notices/{id}/reject":          "closes a notice with no action: nothing is taken down",
	"POST /v1/admin/moderation/reports/{id}/dismiss":         "closes a report with no action: nothing is taken down",
	"POST /v1/admin/moderation/counter-notices/{id}/lawsuit": "records the claimant's word of a lawsuit: keeps a takedown as it is",
}

// IsSensitive reports whether route (chi's pattern) with method needs a fresh MFA check.
func IsSensitive(method, route string) bool { return Sensitive[method+" "+route] }
