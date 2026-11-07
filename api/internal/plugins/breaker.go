package plugins

// The plugins' circuit breaker for jobs joined machines run (story 54.09 §2, 54.42 §4).
// A machine's pipeline cannot write plugin_health (plugin_tables_guard), so it sends each
// plugin job's outcome to the home, which records it here with the same statements as
// the home's own dispatcher (pipeline/src/maktaba_pipeline/plugins/breaker.py; a test
// holds the two texts equal) and the same rule to trip (shouldTrip, on shared vectors).

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// The breaker's numbers (breaker.py's).
const (
	breakerWindowS    = 600
	breakerCrashLimit = 5
	breakerLastN      = 20
	breakerRefusals   = 50
)

// recordSQL and tripSQL are breaker.py's _RECORD_PG and _TRIP_PG, as written there.
const recordSQL = `INSERT INTO plugin_health AS h (plugin_id, recent, crashes, refusals, jobs_done, jobs_failed,
    last_error_code, last_error_at, updated_at)
VALUES ($1, jsonb_build_array($2::jsonb),
    CASE WHEN $3 THEN jsonb_build_array($2::jsonb) ELSE '[]'::jsonb END,
    CASE WHEN $4 > 0 THEN jsonb_build_array(jsonb_build_object('at', $5::float8, 'n', $4))
        ELSE '[]'::jsonb END,
    CASE WHEN $6 = '' THEN 1 ELSE 0 END, CASE WHEN $6 = '' THEN 0 ELSE 1 END,
    $6, CASE WHEN $6 = '' THEN NULL ELSE now() END, now())
ON CONFLICT (plugin_id) DO UPDATE SET
    recent = (SELECT COALESCE(jsonb_agg(e ORDER BY o), '[]'::jsonb) FROM (
        SELECT e, o FROM jsonb_array_elements(h.recent || jsonb_build_array($2::jsonb))
            WITH ORDINALITY AS t(e, o) ORDER BY o DESC LIMIT $7) x),
    crashes = (SELECT COALESCE(jsonb_agg(e), '[]'::jsonb) FROM jsonb_array_elements(
        h.crashes || CASE WHEN $3 THEN jsonb_build_array($2::jsonb) ELSE '[]'::jsonb END) e
        WHERE (e->>'at')::float8 > $5::float8 - $8),
    refusals = (SELECT COALESCE(jsonb_agg(e), '[]'::jsonb) FROM jsonb_array_elements(
        h.refusals || CASE WHEN $4 > 0
            THEN jsonb_build_array(jsonb_build_object('at', $5::float8, 'n', $4))
            ELSE '[]'::jsonb END) e WHERE (e->>'at')::float8 > $5::float8 - $8),
    jobs_done = h.jobs_done + CASE WHEN $6 = '' THEN 1 ELSE 0 END,
    jobs_failed = h.jobs_failed + CASE WHEN $6 = '' THEN 0 ELSE 1 END,
    last_error_code = CASE WHEN $6 = '' THEN h.last_error_code ELSE $6 END,
    last_error_at = CASE WHEN $6 = '' THEN h.last_error_at ELSE now() END,
    updated_at = now()
RETURNING recent, crashes, refusals, tripped
`

const tripSQL = `UPDATE plugin_health SET tripped = true, tripped_at = now(), trip_reason = $2, updated_at = now()
WHERE plugin_id = $1 AND NOT tripped RETURNING plugin_id
`

// Outcome is one plugin job's end, as a machine's dispatcher reports it.
type Outcome struct {
	PluginID string `json:"plugin_id"`
	// Cause is "" for a job that ended well, else its failure cause.
	Cause    string `json:"cause"`
	Ms       int64  `json:"ms"`
	Memory   int64  `json:"memory"`
	Refusals int64  `json:"refusals"`
}

type breakerEntry struct {
	At    float64 `json:"at"`
	Cause string  `json:"cause"`
	N     int64   `json:"n"`
}

// shouldTrip is breaker.py's should_trip: why the breaker trips ("" when it does not).
func shouldTrip(recent, crashes, refusals []breakerEntry, now float64) string {
	n := 0
	for _, c := range crashes {
		if c.At > now-breakerWindowS {
			n++
		}
	}
	if n >= breakerCrashLimit {
		return "crashes"
	}
	if len(recent) >= breakerLastN {
		failed := 0
		for _, r := range recent[len(recent)-breakerLastN:] {
			if strings.HasPrefix(r.Cause, "plugin_") {
				failed++
			}
		}
		if failed*2 > breakerLastN {
			return "failures"
		}
	}
	var refused int64
	for _, r := range refusals {
		if r.At > now-breakerWindowS {
			refused += r.N
		}
	}
	if refused >= breakerRefusals {
		return "refusals"
	}
	return ""
}

var crashCauses = map[string]bool{"plugin_trap": true, "plugin_timeout": true, "plugin_memory": true}

// RecordOutcome records o in its plugin's breaker and trips it when the windows say so,
// holding the plugin through trip (Lifecycle.Trip). It answers why it tripped now.
func RecordOutcome(ctx context.Context, db querier, o Outcome, now time.Time, trip func(ctx context.Context, id, why string) error) (string, error) {
	at := float64(now.UnixNano()) / 1e9
	entry, _ := json.Marshal(map[string]any{"at": at, "cause": o.Cause, "ms": o.Ms, "memory": o.Memory})
	var recent, crashes, refusals []byte
	var tripped bool
	if err := db.QueryRowContext(ctx, recordSQL, o.PluginID, string(entry), crashCauses[o.Cause], o.Refusals, at, o.Cause,
		breakerLastN, float64(breakerWindowS)).Scan(&recent, &crashes, &refusals, &tripped); err != nil {
		return "", err
	}
	if tripped {
		return "", trip(ctx, o.PluginID, "")
	}
	var r, c, f []breakerEntry
	_ = json.Unmarshal(recent, &r)
	_ = json.Unmarshal(crashes, &c)
	_ = json.Unmarshal(refusals, &f)
	why := shouldTrip(r, c, f, at)
	if why == "" {
		return "", nil
	}
	var id string
	if err := db.QueryRowContext(ctx, tripSQL, o.PluginID, why).Scan(&id); err != nil {
		return "", nil // another job tripped it first
	}
	return why, trip(ctx, o.PluginID, why)
}

// BreakerSQL is the two statements, for the test that holds them equal to breaker.py's.
func BreakerSQL() string { return recordSQL + "\n" + tripSQL }
