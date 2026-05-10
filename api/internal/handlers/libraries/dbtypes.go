package libraries

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// stringArray adapts a TEXT[] column (Postgres) or a JSON-encoded array
// (SQLite) into a Go []string. The Postgres lib/pq driver returns the
// column as a string of the form `{a,b,c}` which we have to parse, but
// it also accepts the same shape on write via the same driver.Value path.
type stringArray []string

func (s stringArray) Value() (driver.Value, error) {
	// Postgres array literal — quote each element and escape quotes
	// inside it. We emit `{"a","b"}` rather than the unquoted form so
	// elements with commas survive.
	if len(s) == 0 {
		return "{}", nil
	}
	parts := make([]string, len(s))
	for i, v := range s {
		v = strings.ReplaceAll(v, "\\", "\\\\")
		v = strings.ReplaceAll(v, `"`, `\"`)
		parts[i] = `"` + v + `"`
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func (s *stringArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = nil
		return nil
	case []byte:
		return s.scanText(string(v))
	case string:
		return s.scanText(v)
	}
	return fmt.Errorf("stringArray.Scan: unsupported type %T", src)
}

// scanText understands both Postgres ``{a,b,c}`` and a JSON array
// emitted by the SQLite shim (which stores TEXT[] as a JSON string).
func (s *stringArray) scanText(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(v, "[") {
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return fmt.Errorf("stringArray.Scan json: %w", err)
		}
		*s = out
		return nil
	}
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return errors.New("stringArray.Scan: not a postgres array literal")
	}
	inner := v[1 : len(v)-1]
	if inner == "" {
		*s = nil
		return nil
	}
	// minimal parser — handle quoted and unquoted elements
	out := []string{}
	i := 0
	for i < len(inner) {
		if inner[i] == '"' {
			j := i + 1
			var buf strings.Builder
			for j < len(inner) {
				if inner[j] == '\\' && j+1 < len(inner) {
					buf.WriteByte(inner[j+1])
					j += 2
					continue
				}
				if inner[j] == '"' {
					break
				}
				buf.WriteByte(inner[j])
				j++
			}
			out = append(out, buf.String())
			i = j + 1
			if i < len(inner) && inner[i] == ',' {
				i++
			}
		} else {
			j := i
			for j < len(inner) && inner[j] != ',' {
				j++
			}
			out = append(out, inner[i:j])
			i = j
			if i < len(inner) && inner[i] == ',' {
				i++
			}
		}
	}
	*s = out
	return nil
}

// DeepMergeJSON deep-merges b into a (both JSON objects). Story 7.3
// AC-3: nested keys not mentioned in b are preserved. Arrays are
// replaced wholesale — there's no good "merge arrays" semantic in a
// schemaless setting.
func DeepMergeJSON(a, b json.RawMessage) (json.RawMessage, error) {
	var aMap, bMap map[string]any
	if err := json.Unmarshal(a, &aMap); err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	if err := json.Unmarshal(b, &bMap); err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}
	deepMergeMap(aMap, bMap)
	return json.Marshal(aMap)
}

func deepMergeMap(dst, src map[string]any) {
	for k, v := range src {
		if av, ok := dst[k]; ok {
			if asub, aok := av.(map[string]any); aok {
				if bsub, bok := v.(map[string]any); bok {
					deepMergeMap(asub, bsub)
					continue
				}
			}
		}
		dst[k] = v
	}
}

// isUniqueViolation returns true when err looks like a uniqueness
// failure on the named column. We pattern-match strings instead of
// importing pq so the same handler runs against the SQLite test driver
// (which returns a different error type).
func isUniqueViolation(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "unique") {
		return false
	}
	if column != "" && !strings.Contains(msg, strings.ToLower(column)) {
		// Some drivers report only "constraint failed: <table>.<col>"
		// or "duplicate key value". Best-effort check.
		if !strings.Contains(msg, "duplicate") && !strings.Contains(msg, "constraint") {
			return false
		}
	}
	return true
}
