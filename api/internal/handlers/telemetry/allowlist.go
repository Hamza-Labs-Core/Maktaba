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

import (
	"math"
	"regexp"
	"strings"
)

// AllowedKind is an event kind an app may send and the payload fields kept
// for it. Declared in code, so the allow-list ships with the binary: a release
// extends it, never the runtime.
type AllowedKind struct {
	Kind   string
	Fields []string
}

// Allowed is the event allow-list. A kind not here is refused (400
// unknown-event-kind), and so is a field not listed for its kind (400
// unknown-field).
//
// Nothing here can carry a filename, a title, a transcript, a search query or a
// user: counts, durations, flags and short enumerations, plus error.uncaught's
// error kind (its class, never its message) and its code frames (a function and
// a source file of the app's own, never a path or an address).
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

// CheckFields is the payload when every field is one kind allows and every
// value a scalar (a string, a number, a bool); otherwise the field that is not,
// and the event is refused (the hard rule: nothing but the allow-list leaves an
// app, so a field the list does not name is an error, never a silent drop). An
// object or a list could carry anything.
func CheckFields(kind string, payload map[string]any) (map[string]any, string) {
	f, ok := allowedSet[kind]
	if !ok {
		return nil, ""
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		if _, ok := f[k]; !ok {
			return nil, k
		}
		switch v.(type) {
		case string, float64, bool:
			out[k] = v
		case nil:
		default:
			return nil, k
		}
	}
	return out, ""
}

// What Maktaba Cloud accepts of each field (a batch with one value it refuses
// is refused whole, so the home sends only what it accepts).
var (
	codeRe    = regexp.MustCompile(`[^A-Za-z0-9._:-]`)
	featureRe = regexp.MustCompile(`[^a-z0-9_]+`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,39}$`)
	localeRe  = regexp.MustCompile(`^[A-Za-z]{2,3}(?:[_-][A-Za-z0-9]{1,8}){0,3}$`)
	sources   = map[string]bool{"direct": true, "transcode": true, "live": true}
)

const (
	maxCount    = 1e6
	maxDuration = 7 * 24 * 3600 * 1000
)

// Normalize is an allowed event's payload as Maktaba Cloud takes it: each
// field to its kind (a feature's name to [a-z0-9_], a code to its characters,
// a count or a duration to a whole number in range), what cannot be one
// dropped. Free text (an error's message and frames) is left for the
// scrubbing. ok false: the event says nothing without the field it lacks (a
// feature.used without a feature).
func Normalize(kind string, payload map[string]any) (out map[string]any, ok bool) {
	out = make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	for k, v := range out {
		var keep any
		switch k {
		case "cold_start":
			if b, isBool := v.(bool); isBool {
				keep = b
			}
		case "feature":
			if s, isStr := v.(string); isStr {
				s = strings.Trim(featureRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
				if s != "" {
					keep = clip(s, 40)
				}
			}
		case "source":
			if s, isStr := v.(string); isStr && sources[s] {
				keep = s
			}
		case "codec", "error_kind":
			if s, isStr := v.(string); isStr {
				if s = clip(codeRe.ReplaceAllString(s, ""), 64); s != "" {
					keep = s
				}
			}
		case "count", "duration_ms":
			if f, isNum := v.(float64); isNum && !math.IsNaN(f) && !math.IsInf(f, 0) {
				limit := float64(maxCount)
				if k == "duration_ms" {
					limit = maxDuration
				}
				keep = math.Min(math.Max(math.Round(f), 0), limit)
			}
		case "error_message":
			if s, isStr := v.(string); isStr {
				keep = ErrorKind(s)
			}
		case "stack_first_5":
			if s, isStr := v.(string); isStr {
				if s = CodeFrames(s); s != "" {
					keep = s
				}
			}
		}
		if keep == nil {
			delete(out, k)
		} else {
			out[k] = keep
		}
	}
	if kind == "feature.used" && out["feature"] == nil {
		return nil, false
	}
	return out, true
}

var (
	// an error's class at the start of its message: "TypeError: …" → TypeError
	errorClass = regexp.MustCompile(`^\s*(?:Uncaught\s+)?([A-Za-z_$][A-Za-z0-9_$.]{0,63})\s*(?::|$)`)
	// a frame: "at fn (https://home:8088/assets/a.js:1:2)", "at https://…/a.js:1:2",
	// "fn@https://…/a.js:1:2"; the function and the source file's own name stay
	frameV8      = regexp.MustCompile(`^\s*at\s+(?:(?:async\s+)?([A-Za-z0-9_$.<>\[\] ]{1,80}?)\s+\()?(\S+?):(\d{1,7})(?::(\d{1,7}))?\)?\s*$`)
	frameGecko   = regexp.MustCompile(`^\s*([A-Za-z0-9_$.<>/]{0,80})@(\S+?):(\d{1,7})(?::(\d{1,7}))?\s*$`)
	codeFile     = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}\.(?:js|mjs|cjs|ts|tsx|jsx|swift|kt|java|go)$`)
	functionName = regexp.MustCompile(`[^A-Za-z0-9_$.<>]`)
)

// ErrorKind is what is kept of an error's message: its class (TypeError,
// ChunkLoadError), never its text, which could name a title, a file or a
// person. "Error" when there is no class.
func ErrorKind(message string) string {
	if m := errorClass.FindStringSubmatch(message); m != nil {
		return m[1]
	}
	return "Error"
}

// CodeFrames keeps of a stack the frames in the app's own code, each as
// "function (file.js:line:col)": the file's name only (never its folder, its
// origin or its query), and no frame whose file is not a source file (a
// library's, a user's). At most five.
func CodeFrames(stack string) string {
	var out []string
	for _, line := range strings.Split(stack, "\n") {
		if len(out) == 5 {
			break
		}
		m := frameV8.FindStringSubmatch(line)
		if m == nil {
			m = frameGecko.FindStringSubmatch(line)
		}
		if m == nil {
			continue
		}
		file := m[2]
		if i := strings.IndexAny(file, "?#"); i >= 0 {
			file = file[:i]
		}
		if i := strings.LastIndexAny(file, "/\\"); i >= 0 {
			file = file[i+1:]
		}
		if !codeFile.MatchString(file) {
			continue
		}
		fn := functionName.ReplaceAllString(m[1], "")
		if fn == "" {
			fn = "<anonymous>"
		}
		pos := m[3]
		if m[4] != "" {
			pos += ":" + m[4]
		}
		out = append(out, fn+" ("+file+":"+pos+")")
	}
	return strings.Join(out, "\n")
}

// version is v if Maktaba Cloud takes it as a version, else fallback.
func version(v, fallback string) string {
	if v = strings.TrimSpace(v); versionRe.MatchString(v) {
		return v
	}
	return fallback
}

// locale is l if it is a language tag, else "".
func locale(l string) string {
	if l = strings.TrimSpace(l); len(l) <= 20 && localeRe.MatchString(l) {
		return l
	}
	return ""
}
