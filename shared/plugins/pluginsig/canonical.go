package pluginsig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Canonical JSON (story 54.14 §2): what every signature covers, byte for byte the same in
// Go, Rust (plugind/crates/sig) and TypeScript (store/signing), checked on the shared
// vectors in shared/plugins/testdata/signatures/canonical.json.
//
//   - objects: keys sorted by their UTF-8 bytes, no key twice, no spaces;
//   - strings: valid UTF-8, written as is except `"` `\` and the control characters below
//     U+0020 (\b \f \n \r \t, the rest \u00xx in lowercase hex); nothing else is escaped
//     (not `<`, `>`, `&`, U+2028 or U+2029, which Go's encoding/json would);
//   - numbers: integers only, within ±(2^53 − 1), written in decimal (a fraction, an
//     exponent, a leading zero or -0 is refused);
//   - true, false, null; arrays in their order.
//
// A text that is not already in this form is not refused by Canonical (files.json from an
// older writer is re-encoded); only values the form cannot carry are.

// ErrCanonical is a value canonical JSON cannot carry.
var ErrCanonical = errors.New("not canonical JSON")

const maxSafeInt = 1<<53 - 1

var intRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// value is a parsed JSON value: map[string]value (with its keys' order lost), []value,
// string, json.Number (an integer), bool or nil.
type value = any

// parseJSON reads one JSON value, refusing a key twice, a non-integer and trailing data.
func parseJSON(raw []byte) (value, error) {
	if err := precheck(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the value", ErrCanonical)
	}
	return v, nil
}

// precheck refuses what the three languages' JSON readers would read differently: bytes
// that are not UTF-8 (Go would replace them) and a \u escape of a surrogate, paired or
// not (Go replaces a lone one, Rust refuses it, JavaScript keeps it).
func precheck(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("%w: the text is not UTF-8", ErrCanonical)
	}
	in := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case !in && c == '"':
			in = true
		case in && c == '"':
			in = false
		case in && c == '\\' && i+1 < len(raw):
			if raw[i+1] == 'u' && i+5 < len(raw) {
				if n, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 32); err == nil && n >= 0xD800 && n <= 0xDFFF {
					return fmt.Errorf("%w: a surrogate escape", ErrCanonical)
				}
			}
			i++
		}
	}
	return nil
}

func parseValue(dec *json.Decoder) (value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCanonical, err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := map[string]value{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrCanonical, err)
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("%w: a key is not a string", ErrCanonical)
				}
				if _, dup := obj[k]; dup {
					return nil, fmt.Errorf("%w: the key %q comes twice", ErrCanonical, k)
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				obj[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrCanonical, err)
			}
			return obj, nil
		case '[':
			arr := []value{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrCanonical, err)
			}
			return arr, nil
		}
		return nil, fmt.Errorf("%w: unexpected %v", ErrCanonical, t)
	case json.Number:
		if err := checkInt(string(t)); err != nil {
			return nil, err
		}
		return t, nil
	case string, bool, nil:
		return t, nil
	}
	return nil, fmt.Errorf("%w: unexpected token %v", ErrCanonical, tok)
}

func checkInt(s string) error {
	if !intRe.MatchString(s) || s == "-0" {
		return fmt.Errorf("%w: %s is not an integer", ErrCanonical, s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > maxSafeInt || n < -maxSafeInt {
		return fmt.Errorf("%w: %s is out of range", ErrCanonical, s)
	}
	return nil
}

// Canonical answers the canonical form of the JSON text raw.
func Canonical(raw []byte) ([]byte, error) {
	v, err := parseJSON(raw)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// CanonicalOf answers the canonical form of a Go value (through encoding/json).
func CanonicalOf(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Canonical(raw)
}

func writeCanonical(b *bytes.Buffer, v value) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		if err := checkInt(string(t)); err != nil {
			return err
		}
		b.WriteString(string(t))
	case string:
		return writeString(b, t)
	case []value:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]value:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // Go compares strings by their bytes
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeString(b, k); err != nil {
				return err
			}
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("%w: a %T", ErrCanonical, v)
	}
	return nil
}

func writeString(b *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: a string is not UTF-8", ErrCanonical)
	}
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return nil
}
