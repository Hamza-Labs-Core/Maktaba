package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// VersionPrefix is the app_settings key prefix of the schema version a section's stored
// value was written at (Epic 51, story 51.13): {"major","minor"}, plus "previous" (the
// value before the home's migration) and "failed" (a migration that could not run). A
// missing key is 1.0. It is a key of its own so the section's value stays as older
// releases read it, and LIKE 'rules.%' never picks it up.
const VersionPrefix = "rules_version."

// Version is a section's schema version, from schema.json or as stored.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// Of is the version a section of the schema declares (1.0 when it declares none).
func Of(sec *Section) Version {
	if sec == nil || len(sec.Version) != 2 || sec.Version[0] < 1 || sec.Version[1] < 0 {
		return Version{Major: 1}
	}
	return Version{Major: sec.Version[0], Minor: sec.Version[1]}
}

// storedVersion is a rules_version.<section> row as decoded.
type storedVersion struct {
	Version
	Previous json.RawMessage `json:"previous,omitempty"`
	Failed   string          `json:"failed,omitempty"`
	exists   bool
}

func decodeVersion(raw []byte) storedVersion {
	v := storedVersion{Version: Version{Major: 1}, exists: true}
	var m struct {
		Major    *int            `json:"major"`
		Minor    *int            `json:"minor"`
		Previous json.RawMessage `json:"previous"`
		Failed   string          `json:"failed"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return v
	}
	if m.Major != nil && *m.Major >= 1 {
		v.Major = *m.Major
	}
	if m.Minor != nil && *m.Minor >= 0 {
		v.Minor = *m.Minor
	}
	if len(m.Previous) > 0 && string(m.Previous) != "null" {
		v.Previous = m.Previous
	}
	v.Failed = m.Failed
	return v
}

func loadVersion(ctx context.Context, q querier, section string) (storedVersion, error) {
	rows, err := q.QueryContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, VersionPrefix+section)
	if err != nil {
		return storedVersion{}, err
	}
	defer rows.Close()
	v := storedVersion{Version: Version{Major: 1}}
	if rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return v, err
		}
		v = decodeVersion(raw)
	}
	return v, rows.Err()
}

func loadVersions(ctx context.Context, q querier) (map[string]storedVersion, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM app_settings WHERE key LIKE 'rules_version.%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedVersion{}
	for rows.Next() {
		var k string
		var raw []byte
		if err := rows.Scan(&k, &raw); err != nil {
			return nil, err
		}
		if strings.HasPrefix(k, VersionPrefix) {
			out[strings.TrimPrefix(k, VersionPrefix)] = decodeVersion(raw)
		}
	}
	return out, rows.Err()
}

var (
	ownersOnce sync.Once
	owners     map[string]string
)

// Owner is the id of the component whose catalog settings name rules.<section>, or ""
// (a section no component names belongs to core, always on).
func Owner(section string) string {
	ownersOnce.Do(func() {
		owners = map[string]string{}
		for _, c := range components.Default().Components() {
			for _, s := range c.Settings {
				if name, ok := strings.CutPrefix(s, KeyPrefix); ok {
					owners[name] = c.ID
				}
			}
		}
	})
	return owners[section]
}

// SectionsOf is the rules sections component id owns, in the catalog's order.
func SectionsOf(id string) []string {
	c, ok := components.Default().Get(id)
	if !ok {
		return nil
	}
	out := []string{}
	for _, s := range c.Settings {
		if name, ok := strings.CutPrefix(s, KeyPrefix); ok {
			out = append(out, name)
		}
	}
	return out
}

// componentStates is each component's state as the Rules page names it: enabled, off
// (installed), absent or held. No row is enabled. A database without the components
// table gives none.
func componentStates(ctx context.Context, db *sql.DB) map[string]string {
	out := map[string]string{}
	if db == nil {
		return out
	}
	rows, err := db.QueryContext(ctx, `SELECT id, want, hold_at IS NOT NULL FROM components`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, want string
		var held bool
		if rows.Scan(&id, &want, &held) != nil {
			return out
		}
		switch {
		case want == components.WantInstalled:
			out[id] = "off"
		case want == components.WantAbsent:
			out[id] = "absent"
		case held:
			out[id] = "held"
		default:
			out[id] = "enabled"
		}
	}
	return out
}
