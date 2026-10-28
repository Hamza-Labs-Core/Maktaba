package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Shared SQL fragments and scanners. Everything that renders a subject
// label or a JobRef goes through here so the REST payloads, the SSE
// payloads and the history timeline cannot drift apart.

// liveStatesSQL are the states covered by the live-row unique index.
const liveStatesSQL = `('pending','claimed','running','resuming','paused')`

// inFlightSQL are the states that occupy a worker slot.
const inFlightSQL = `('claimed','running','resuming')`

// labelSQL returns the LEFT JOINs and the SELECT expression that turn a
// (kind, id) pair of columns into a human label (plan-06-11 §8.3).
func labelSQL(kindCol, idCol string) (joins, expr string) {
	joins = fmt.Sprintf(`
	LEFT JOIN videos lv ON %[1]s = 'video' AND lv.id = %[2]s
	LEFT JOIN libraries ll ON %[1]s = 'library' AND ll.id = %[2]s
	LEFT JOIN series_seasons lss ON %[1]s = 'season' AND lss.id = %[2]s
	LEFT JOIN series lsr ON %[1]s = 'series' AND lsr.id = %[2]s`, kindCol, idCol)
	// a duplicate group is named by its keeper, else its first member (pipeline
	// engine/subjects.py label_of)
	expr = fmt.Sprintf(`COALESCE(CASE %[1]s
		WHEN 'video' THEN COALESCE(lv.title, lv.filename)
		WHEN 'dup_group' THEN (SELECT COALESCE(gkv.title, gkv.filename) FROM duplicate_groups gk
			JOIN videos gkv ON gkv.id = COALESCE(gk.keeper_video_id,
				(SELECT gkm.video_id FROM duplicate_members gkm WHERE gkm.group_id = gk.id ORDER BY gkm.video_id LIMIT 1))
			WHERE gk.id = %[2]s)
		WHEN 'library' THEN ll.name
		WHEN 'season' THEN CASE WHEN lss.kind = 'ongoing'
			THEN lss.name || ' · ' || COALESCE(to_char(lss.first_air, 'YYYY'), 'S' || lss.season::text)
			ELSE lss.name || ' · S' || lss.season::text END
		WHEN 'series' THEN COALESCE(lsr.name_override, lsr.name)
		WHEN 'catalog' THEN 'Catalog'
	END, %[2]s::text)`, kindCol, idCol)
	return joins, expr
}

// jobRefQuery builds SELECT <JobRef columns> FROM processing_jobs j <joins>.
// extra columns (already comma-prefixed) are appended to the select list.
func jobRefQuery(extraCols string) string {
	joins, expr := labelSQL("j.subject_kind", "j.subject_id")
	return `SELECT j.id, j.stage, j.state, j.subject_kind, j.subject_id::text, ` + expr + `,
	       j.library_id::text, j.priority, j.attempts, j.max_attempts, j.outcome,
	       j.error_cause, j.created_at, j.started_at, j.finished_at, j.progress, j.claimed_by` + extraCols + `
	  FROM processing_jobs j` + joins
}

type rowScanner interface {
	Scan(...any) error
}

// scanJobRef scans the columns of jobRefQuery; dest receives any extra
// trailing columns.
func scanJobRef(rs rowScanner, dest ...any) (JobRef, error) {
	var j JobRef
	var lib, outcome, cause sql.NullString
	var started, finished sql.NullTime
	var progress []byte
	var claimedBy sql.NullString
	args := []any{&j.ID, &j.Stage, &j.State, &j.SubjectKind, &j.SubjectID, &j.SubjectLabel,
		&lib, &j.Priority, &j.Attempts, &j.MaxAttempts, &outcome, &cause,
		&j.CreatedAt, &started, &finished, &progress, &claimedBy}
	args = append(args, dest...)
	if err := rs.Scan(args...); err != nil {
		return JobRef{}, err
	}
	if lib.Valid {
		j.LibraryID = &lib.String
	}
	if outcome.Valid {
		j.Outcome = &outcome.String
	}
	if cause.Valid {
		j.ErrorCause = &cause.String
	}
	if started.Valid {
		t := started.Time
		j.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		j.FinishedAt = &t
	}
	j.Progress = parseProgress(progress)
	j.ClaimedBy = nullStrPtr(claimedBy)
	return j, nil
}

func parseProgress(b []byte) *Progress {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var p Progress
	if err := json.Unmarshal(b, &p); err != nil {
		return nil
	}
	if p.Steps == nil {
		p.Steps = []string{}
	}
	return &p
}

// queryJobRefs runs a jobRefQuery-based statement.
func queryJobRefs(ctx context.Context, db *sql.DB, q string, args ...any) ([]JobRef, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobRef{}
	for rows.Next() {
		j, err := scanJobRef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// subjectKey identifies a subject for batched label lookups.
type subjectKey struct{ Kind, ID string }

// labelsFor resolves labels for many subjects in one statement.
func labelsFor(ctx context.Context, db *sql.DB, keys []subjectKey) (map[subjectKey]string, error) {
	out := map[subjectKey]string{}
	if len(keys) == 0 {
		return out, nil
	}
	seen := map[subjectKey]bool{}
	kinds := make([]string, 0, len(keys))
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		if seen[k] || !subjectKinds[k.Kind] || !isUUID(k.ID) {
			continue
		}
		seen[k] = true
		kinds = append(kinds, k.Kind)
		ids = append(ids, k.ID)
	}
	if len(kinds) == 0 {
		return out, nil
	}
	joins, expr := labelSQL("k.kind", "k.id")
	rows, err := db.QueryContext(ctx, `SELECT k.kind, k.id::text, `+expr+`
	  FROM unnest($1::text[], $2::uuid[]) AS k(kind, id)`+joins,
		pq.Array(kinds), pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k subjectKey
		var label string
		if err := rows.Scan(&k.Kind, &k.ID, &label); err != nil {
			return nil, err
		}
		out[k] = label
	}
	return out, rows.Err()
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

// parseSubjectKey splits "kind:uuid" values produced by array_agg.
func parseSubjectKey(s string) (subjectKey, bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return subjectKey{}, false
	}
	return subjectKey{Kind: s[:i], ID: s[i+1:]}, true
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func nullStrPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// errorText extracts a readable message from processing_jobs.error,
// which is JSON {kind,message,traceback,retryable} on newer rows and a
// plain string on older ones.
func errorText(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var e struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &e) == nil && (e.Message != "" || e.Kind != "") {
			if e.Message != "" {
				return truncate(e.Message, 300)
			}
			return e.Kind
		}
	}
	return truncate(raw, 300)
}

// legacyCause groups rows written before error_cause existed by the
// JSON "kind" of their error; plain-text errors fall into "unknown".
func legacyCause(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var e struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal([]byte(raw), &e) == nil && e.Kind != "" {
			return e.Kind
		}
	}
	return "unknown"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// cursor helpers: keyset cursors are the decimal id of the last row.
func parseCursor(s string) (int64, bool) {
	if s == "" {
		return 0, true
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
		if n < 0 {
			return 0, false
		}
	}
	return n, true
}
