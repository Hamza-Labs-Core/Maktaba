// Package secret centralises Maktaba's handling of sensitive
// configuration values (Story 10.14).
//
// Two responsibilities live here:
//
//  1. Loading. Env-var values win over file-based config; when both
//     define the same key, the file value is silently ignored *with a
//     log line* so an operator can spot the override.
//
//  2. Redaction. A typed `Value` wraps a string so that slog, fmt, and
//     JSON encoders all render `<redacted>` instead of the plaintext.
//     Settings handlers walk a struct via reflection and emit
//     `<redacted>` for every `secret:"true"`-tagged field plus a
//     sibling `*_present` boolean (Story 10.14 AC-3).
package secret

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// RedactedString is the canonical placeholder Maktaba emits whenever a
// secret would otherwise be printed. Kept lowercase to match the
// architecture spec phrasing ("<redacted>", §11.5).
const RedactedString = "<redacted>"

// Value wraps a sensitive string. Once a piece of config is parsed
// into a Value, the only legal way to read its plaintext is .Reveal();
// all other read paths (fmt, JSON, slog) render the placeholder.
//
// The zero value is empty and "absent": Present() returns false.
type Value struct {
	v       string
	present bool
}

// New constructs a Value from a plaintext secret. An empty string is
// treated as "not configured" so callers can write `if s.Present()`
// without juggling pointers.
func New(s string) Value {
	if s == "" {
		return Value{}
	}
	return Value{v: s, present: true}
}

// Reveal returns the underlying plaintext. Use sparingly — every call
// site is a potential source of a leak (an `Authorization` header, a
// log line, an error message). The verifier and signer in
// internal/auth/jwt are the legitimate consumers; everything else
// should hold the Value.
func (v Value) Reveal() string { return v.v }

// Present reports whether the secret was configured.
func (v Value) Present() bool { return v.present }

// String satisfies fmt.Stringer so `fmt.Sprintf("%s", v)` does not
// leak the plaintext.
func (v Value) String() string {
	if !v.present {
		return ""
	}
	return RedactedString
}

// GoString satisfies fmt.GoStringer so `%#v` is also safe.
func (v Value) GoString() string { return v.String() }

// LogValue makes Value an slog.LogValuer; logged keys whose value is a
// Value render as `<redacted>` regardless of the key name.
func (v Value) LogValue() slog.Value {
	if !v.present {
		return slog.StringValue("")
	}
	return slog.StringValue(RedactedString)
}

// MarshalJSON serialises a Value as the redacted string, never the
// plaintext. This is what makes the /api/settings response inherently
// safe — handlers can reflect-marshal the same struct they keep in
// memory.
func (v Value) MarshalJSON() ([]byte, error) {
	if !v.present {
		return []byte(`""`), nil
	}
	return json.Marshal(RedactedString)
}

// FromEnvOrFile resolves a secret per the precedence rule in
// Story 10.14 AC-1: env-var beats file-based config; when both are set
// the file value is ignored and a log line records the override.
//
// `envName` is the env-var key. `fileVal` is whatever the TOML/JSON
// loader read; pass an empty string when the file did not define the
// key. `logger` may be nil during early bootstrap.
func FromEnvOrFile(envName, fileVal string, logger *slog.Logger) Value {
	envVal := os.Getenv(envName)
	if envVal != "" {
		if fileVal != "" && logger != nil {
			logger.Info("secret: env override wins; file value ignored",
				"env", envName,
				"event", "secret_override",
			)
		}
		return New(envVal)
	}
	return New(fileVal)
}

// nameLooksSensitive matches keys that look like a secret by name even
// when no struct tag is set (Story 10.14 EC-1). The list is the same
// substrings the slog redactor uses.
var sensitiveSubstrings = []string{
	"password", "passwd", "pwd",
	"secret", "token", "apikey", "api_key",
	"authorization", "auth", "cookie",
	"private_key", "client_secret",
}

func nameLooksSensitive(name string) bool {
	low := strings.ToLower(name)
	for _, s := range sensitiveSubstrings {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// RedactSettings walks `in` (a struct, struct pointer, or
// map[string]any) and returns a JSON-serialisable map where every
// secret-bearing field is `<redacted>` and a sibling `<field>_present`
// boolean indicates whether the secret was configured (Story 10.14
// AC-3). The original value is never mutated.
//
// Field selection rules (in order):
//
//  1. `secret:"true"` tag — always redacted.
//  2. `notsecret:"true"` tag — never redacted (escape hatch).
//  3. Field name looks sensitive (matches sensitiveSubstrings) —
//     redacted by default.
//
// Nested structs are recursively redacted; maps and slices pass
// through unchanged.
func RedactSettings(in any) map[string]any {
	out := map[string]any{}
	v := reflect.ValueOf(in)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return out
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := jsonName(f)
			redact := false
			if f.Tag.Get("notsecret") == "true" {
				redact = false
			} else if f.Tag.Get("secret") == "true" {
				redact = true
			} else if nameLooksSensitive(name) {
				redact = true
			}

			fv := v.Field(i)
			if redact {
				present := isPresent(fv)
				out[name] = RedactedString
				out[name+"_present"] = present
				continue
			}

			// If the field is itself a Value, render via Value's own
			// rules even if the name didn't trip the sensitive match.
			if val, ok := fv.Interface().(Value); ok {
				out[name] = RedactedString
				out[name+"_present"] = val.Present()
				continue
			}

			// Recurse into nested structs/pointers.
			fk := fv.Kind()
			if fk == reflect.Struct || (fk == reflect.Pointer && !fv.IsNil() && fv.Elem().Kind() == reflect.Struct) {
				out[name] = RedactSettings(fv.Interface())
				continue
			}
			out[name] = fv.Interface()
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			name := fmt.Sprint(k.Interface())
			val := v.MapIndex(k).Interface()
			if nameLooksSensitive(name) {
				out[name] = RedactedString
				out[name+"_present"] = val != nil && fmt.Sprint(val) != ""
				continue
			}
			out[name] = val
		}
	}
	return out
}

// jsonName returns the field's JSON key, falling back to the Go field
// name when the json tag is absent or `-`.
func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return f.Name
	}
	if i := strings.Index(tag, ","); i >= 0 {
		return tag[:i]
	}
	return tag
}

// isPresent treats a field as configured if it's a non-empty string,
// a Value with Present()==true, or any non-zero non-string value.
func isPresent(v reflect.Value) bool {
	if val, ok := v.Interface().(Value); ok {
		return val.Present()
	}
	switch v.Kind() {
	case reflect.String:
		return v.Len() > 0
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return !v.IsNil()
	default:
		return !v.IsZero()
	}
}

// signedURLSig matches a `?sig=...` or `&sig=...` query-string fragment
// up to the next separator. Used by RedactURL for signed-URL paths
// that end up in request logs (Story 10.14 EC-2).
var signedURLSig = regexp.MustCompile(`([?&](?:sig|signature|token|access_token|key)=)[^&\s#]+`)

// RedactURL scrubs known-sensitive query parameters from a URL string
// so it's safe to log. Path segments are preserved (they're how we
// identify which endpoint was hit); only the value of sensitive
// query keys is replaced.
func RedactURL(u string) string {
	return signedURLSig.ReplaceAllString(u, "$1"+RedactedString)
}

// SensitiveHeaders is the canonical list of request/response headers
// whose values must never reach a log file. Used by the request
// logger and by the gRPC interceptor (Story 10.14 AC-4).
var SensitiveHeaders = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
	"Proxy-Authorization",
	"X-Maktaba-CSRF",
	"X-Api-Key",
}

// IsSensitiveHeader reports whether `name` (case-insensitively) is in
// SensitiveHeaders.
func IsSensitiveHeader(name string) bool {
	for _, h := range SensitiveHeaders {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}
