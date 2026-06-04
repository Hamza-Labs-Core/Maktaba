package pat

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// scopeArray adapts the `scopes` column between a Go []string and the
// dialect-specific storage: a Postgres TEXT[] literal (`{a,b}`) or a
// JSON array string under the SQLite parity schema. Mirrors the
// libraries package's stringArray so the same handler code runs against
// both drivers.
type scopeArray []string

func (s scopeArray) Value() (driver.Value, error) {
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

func (s *scopeArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = nil
		return nil
	case []byte:
		return s.scanText(string(v))
	case string:
		return s.scanText(v)
	}
	return fmt.Errorf("scopeArray.Scan: unsupported type %T", src)
}

func (s *scopeArray) scanText(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(v, "[") {
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return fmt.Errorf("scopeArray.Scan json: %w", err)
		}
		*s = out
		return nil
	}
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return errors.New("scopeArray.Scan: not a postgres array literal")
	}
	inner := v[1 : len(v)-1]
	if inner == "" {
		*s = nil
		return nil
	}
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
