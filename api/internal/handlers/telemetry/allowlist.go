// Package telemetry is the opt-in telemetry sink (Epic 16 story 16.7): the
// endpoints a device that opted in (story 16.5) posts its events and Web Vitals
// to, the redaction they pass through, "Forget my device", and the nightly
// retention sweep. The sink is anonymous: rows carry a device pseudonym, never a
// user, and no IP address is stored.
package telemetry

// AllowedKind is an event kind the sink accepts and the payload fields it keeps
// for it. Declared in code, so the allow-list ships with the binary: a release
// extends it, never the runtime.
type AllowedKind struct {
	Kind   string
	Fields []string
}

// Allowed is the event allow-list. A kind not here is refused (400
// unknown-event-kind); a field not listed for its kind is dropped.
//
// Nothing here can carry a filename, a transcript, a search query or a user:
// counts, durations, flags and short enumerations, plus error.uncaught's
// message and first stack frames, which are redacted (redact.go).
var Allowed = []AllowedKind{
	{"app.open", []string{"cold_start", "route", "library_size_bucket"}},
	{"feature.used", []string{"feature", "count"}},
	{"search.run", []string{"result_count", "has_filters", "duration_ms"}},
	{"search.suggest", []string{"suggestion_count"}},
	{"player.start", []string{"source", "codec", "is_hdr"}},
	{"player.error", []string{"error_kind", "duration_ms"}},
	{"library.scan_done", []string{"duration_ms", "added", "removed"}},
	{"transcribe.done", []string{"duration_ms", "model"}},
	{"error.uncaught", []string{"error_message", "stack_first_5"}},
}

// Metrics are the Web Vitals the sink accepts (FID for older clients; INP
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
