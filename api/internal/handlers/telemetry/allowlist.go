// Package telemetry is the home's part of anonymous usage data (Epic 16 stories
// 16.5 and 16.7): what goes to Maktaba Cloud to improve Maktaba, never to the
// home's admin (who already has Epic 44's named analytics).
//
// Two consents, neither implying the other:
//
//   - the server's: the admin's switch (runtime setting
//     telemetry.share_server_data, off by default). While on, the home sends
//     Maktaba Cloud a report a day about itself (report.go): its version and
//     platform, ranges of its size and use, and its errors scrubbed.
//   - each app's: an app's user agrees on that device. The app posts its batch
//     to the home (POST /api/telemetry/app) with that answer; the home scrubs it
//     and forwards it whatever the server switch says. The home is a pipe.
//
// What goes waits in telemetry_outbox (slot 0161) until the sender (sender.go)
// delivers it, with retries; a row a week old is dropped unsent. Ids are
// random per install (the home's, each app's), never a user, and no address
// is kept.
package telemetry

// AllowedKind is an event kind an app may send and the payload fields kept
// for it. Declared in code, so the allow-list ships with the binary: a release
// extends it, never the runtime.
type AllowedKind struct {
	Kind   string
	Fields []string
}

// Allowed is the event allow-list. A kind not here is refused (400
// unknown-event-kind); a field not listed for its kind is dropped.
//
// Nothing here can carry a filename, a title, a transcript, a search query or a
// user: counts, durations, flags and short enumerations, plus error.uncaught's
// message and first stack frames, which are scrubbed (redact.go).
var Allowed = []AllowedKind{
	{"app.open", []string{"cold_start"}},
	{"feature.used", []string{"feature", "count"}},
	{"player.start", []string{"source", "codec"}},
	{"player.error", []string{"error_kind", "duration_ms"}},
	{"error.uncaught", []string{"error_message", "stack_first_5"}},
}

// Metrics are the Web Vitals an app may send (FID for older clients; INP
// replaced it).
var Metrics = map[string]struct{}{"LCP": {}, "FID": {}, "CLS": {}, "INP": {}, "TTFB": {}}

var allowedSet = func() map[string]map[string]struct{} {
	m := make(map[string]map[string]struct{}, len(Allowed))
	for _, a := range Allowed {
		f := make(map[string]struct{}, len(a.Fields))
		for _, x := range a.Fields {
			f[x] = struct{}{}
		}
		m[a.Kind] = f
	}
	return m
}()

// IsKindAllowed reports whether kind is on the allow-list.
func IsKindAllowed(kind string) bool {
	_, ok := allowedSet[kind]
	return ok
}

// StripUnknownFields keeps the payload fields kind allows and whose values are
// scalars (a string, a number, a bool); an object or a list could smuggle
// anything, so it is dropped as an unknown field is. nil for an unknown kind.
func StripUnknownFields(kind string, payload map[string]any) map[string]any {
	f, ok := allowedSet[kind]
	if !ok {
		return nil
	}
	out := make(map[string]any, len(f))
	for k, v := range payload {
		if _, ok := f[k]; !ok {
			continue
		}
		switch v.(type) {
		case string, float64, bool:
			out[k] = v
		}
	}
	return out
}
