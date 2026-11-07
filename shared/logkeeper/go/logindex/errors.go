package logindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Error groups (Epic 53 story 53.10): every error and fatal line gets a fingerprint (the
// cloud's: shared/log/go Fingerprint over the grouping-scrubbed message and the first own
// frame), and lines with the same one are one group, with a count, first and last seen,
// hourly counts, the users it hit and up to 20 recent samples. A group an admin resolved that
// happens again on a later version is new again, marked "came back".

const errorsSchema = `
CREATE TABLE IF NOT EXISTS error_groups (
  fp TEXT PRIMARY KEY, source TEXT NOT NULL, service TEXT NOT NULL, component TEXT, code TEXT,
  title TEXT NOT NULL,
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','seen','resolved','muted')),
  resolved_in TEXT, came_back_in TEXT, muted_until INTEGER,
  last_version TEXT, last_request_id TEXT,
  sample_line_ids TEXT NOT NULL DEFAULT '[]',
  notified_at INTEGER
);
CREATE TABLE IF NOT EXISTS error_hours (fp TEXT NOT NULL, hour INTEGER NOT NULL, count INTEGER NOT NULL,
  PRIMARY KEY (fp, hour));
CREATE TABLE IF NOT EXISTS error_users (fp TEXT NOT NULL, user_h TEXT NOT NULL, PRIMARY KEY (fp, user_h));
CREATE INDEX IF NOT EXISTS error_groups_last ON error_groups(last_seen);
`

// maxSamples is how many recent lines a group keeps; maxUsers how many users it counts.
const (
	maxSamples = 20
	maxUsers   = 1000
)

// fingerprint is the line's group: source server, its service, its code or message with
// the scrubbed error text, its first own frame.
func fingerprint(p parsed) (fp, title string) {
	msg := p.errMsg
	if msg == "" {
		msg = p.rawMsg
	}
	head := p.errCode
	if head == "" {
		head = p.rawMsg
	}
	title = strings.TrimSpace(mlog.GroupScrub(strings.TrimSpace(head + ": " + msg)))
	if p.errMsg == "" && p.errCode == "" {
		title = strings.TrimSpace(mlog.GroupScrub(p.rawMsg))
	}
	if len(title) > 300 {
		title = title[:300] + "…"
	}
	return mlog.Fingerprint("server", p.service, title, p.frame), title
}

// group counts one error line in its group (inside the ingest's transaction).
func (ix *Index) group(tx *sql.Tx, p parsed, lineID int64) (string, error) {
	fp, title := fingerprint(p)
	hour := p.ts / int64(time.Hour/time.Millisecond)
	var status, resolvedIn, samples string
	var mutedUntil sql.NullInt64
	err := tx.QueryRow(`SELECT status, COALESCE(resolved_in, ''), sample_line_ids, muted_until FROM error_groups WHERE fp = ?`, fp).
		Scan(&status, &resolvedIn, &samples, &mutedUntil)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO error_groups(fp, source, service, component, code, title, first_seen, last_seen,
			count, status, last_version, last_request_id, sample_line_ids) VALUES(?, 'server', ?, ?, ?, ?, ?, ?, 1, 'new', ?, ?, ?)`,
			fp, p.service, nullable(p.component), nullable(p.errCode), title, p.ts, p.ts, nullable(p.version),
			nullable(p.requestID), "["+strconv.FormatInt(lineID, 10)+"]"); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		var ids []int64
		_ = json.Unmarshal([]byte(samples), &ids)
		ids = append(ids, lineID)
		if len(ids) > maxSamples {
			ids = ids[len(ids)-maxSamples:]
		}
		sb, _ := json.Marshal(ids)
		next, cameBack := status, ""
		// resolved, then seen again on a later version: new again, "came back"
		if status == "resolved" && p.version != "" && versionLess(resolvedIn, p.version) {
			next, cameBack = "new", p.version
		}
		// a mute for a time ends when its time does
		if status == "muted" && mutedUntil.Valid && mutedUntil.Int64 > 0 && p.ts > mutedUntil.Int64 {
			next = "seen"
		}
		if _, err := tx.Exec(`UPDATE error_groups SET last_seen = MAX(last_seen, ?), count = count + 1, status = ?,
			came_back_in = CASE WHEN ? != '' THEN ? ELSE came_back_in END,
			notified_at = CASE WHEN ? != '' THEN NULL ELSE notified_at END,
			last_version = COALESCE(?, last_version), last_request_id = COALESCE(?, last_request_id),
			sample_line_ids = ? WHERE fp = ?`,
			p.ts, next, cameBack, cameBack, cameBack, nullable(p.version), nullable(p.requestID), string(sb), fp); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(`INSERT INTO error_hours(fp, hour, count) VALUES(?, ?, 1)
		ON CONFLICT(fp, hour) DO UPDATE SET count = count + 1`, fp, hour); err != nil {
		return "", err
	}
	if p.userH != "" {
		var n int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM error_users WHERE fp = ?`, fp).Scan(&n)
		if n < maxUsers {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO error_users(fp, user_h) VALUES(?, ?)`, fp, p.userH); err != nil {
				return "", err
			}
		}
	}
	return fp, nil
}

// versionLess reports whether version a is older than b (0.3.44 < 0.3.45; a leading v and
// a pre-release suffix are ignored; "" is older than anything).
func versionLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// Group is one error group as the API answers it.
type Group struct {
	FP         string `json:"fp"`
	Service    string `json:"service"`
	Component  string `json:"component,omitempty"`
	Code       string `json:"code,omitempty"`
	Title      string `json:"title"`
	FirstSeen  string `json:"first_seen"`
	LastSeen   string `json:"last_seen"`
	Count      int64  `json:"count"`
	Count24h   int64  `json:"count_24h"`
	Users      int64  `json:"users"`
	Status     string `json:"status"`
	ResolvedIn string `json:"resolved_in,omitempty"`
	CameBackIn string `json:"came_back_in,omitempty"`
	MutedUntil string `json:"muted_until,omitempty"`
	Version    string `json:"last_version,omitempty"`
	RequestID  string `json:"last_request_id,omitempty"`
	// Days is the last 14 days' counts, oldest first (the list's sparkline).
	Days []int64 `json:"days,omitempty"`
}

// GroupQuery filters the list.
type GroupQuery struct {
	Status    []string // empty: new and seen
	Service   string
	Component string
	Since     time.Time
	Q         string
	Sort      string // last_seen (default), count, users
	Limit     int
}

func msTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// Groups lists error groups.
func (ix *Index) Groups(ctx context.Context, q GroupQuery) ([]Group, error) {
	status := q.Status
	if len(status) == 0 {
		status = []string{"new", "seen"}
	}
	where := []string{"status IN (" + strings.TrimSuffix(strings.Repeat("?,", len(status)), ",") + ")"}
	var args []any
	for _, s := range status {
		args = append(args, s)
	}
	if q.Service != "" {
		where, args = append(where, "service = ?"), append(args, q.Service)
	}
	if q.Component != "" {
		where, args = append(where, "component = ?"), append(args, q.Component)
	}
	if !q.Since.IsZero() {
		where, args = append(where, "last_seen >= ?"), append(args, q.Since.UnixMilli())
	}
	if q.Q != "" {
		where, args = append(where, "title LIKE ?"), append(args, "%"+q.Q+"%")
	}
	order := "last_seen DESC"
	switch q.Sort {
	case "count":
		order = "count DESC"
	case "users":
		order = "(SELECT COUNT(*) FROM error_users u WHERE u.fp = g.fp) DESC"
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := ix.db.QueryContext(ctx, `SELECT fp FROM error_groups g WHERE `+strings.Join(where, " AND ")+
		` ORDER BY `+order+` LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	var fps []string
	for rows.Next() {
		var fp string
		if rows.Scan(&fp) == nil {
			fps = append(fps, fp)
		}
	}
	_ = rows.Close()
	out := make([]Group, 0, len(fps))
	for _, fp := range fps {
		if g, err := ix.Group(ctx, fp, 14); err == nil {
			out = append(out, g)
		}
	}
	return out, nil
}

// Group is one group, with its last days' counts.
func (ix *Index) Group(ctx context.Context, fp string, days int) (Group, error) {
	var g Group
	var comp, code, resolved, came, ver, rid sql.NullString
	var first, last int64
	var muted sql.NullInt64
	err := ix.db.QueryRowContext(ctx, `SELECT fp, service, component, code, title, first_seen, last_seen, count, status,
		resolved_in, came_back_in, muted_until, last_version, last_request_id FROM error_groups WHERE fp = ?`, fp).
		Scan(&g.FP, &g.Service, &comp, &code, &g.Title, &first, &last, &g.Count, &g.Status, &resolved, &came, &muted, &ver, &rid)
	if err != nil {
		return g, err
	}
	g.Component, g.Code, g.ResolvedIn, g.CameBackIn, g.Version, g.RequestID = comp.String, code.String, resolved.String,
		came.String, ver.String, rid.String
	g.FirstSeen, g.LastSeen = msTime(first), msTime(last)
	if muted.Valid {
		g.MutedUntil = msTime(muted.Int64)
	}
	nowHour := ix.now().UnixMilli() / int64(time.Hour/time.Millisecond)
	_ = ix.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(count), 0) FROM error_hours WHERE fp = ? AND hour > ?`, fp, nowHour-24).
		Scan(&g.Count24h)
	_ = ix.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_users WHERE fp = ?`, fp).Scan(&g.Users)
	if days > 0 {
		g.Days = make([]int64, days)
		rows, err := ix.db.QueryContext(ctx, `SELECT hour, count FROM error_hours WHERE fp = ? AND hour > ?`, fp, nowHour-int64(days*24))
		if err == nil {
			for rows.Next() {
				var h, n int64
				if rows.Scan(&h, &n) == nil {
					d := int((nowHour - h) / 24)
					if d >= 0 && d < days {
						g.Days[days-1-d] += n
					}
				}
			}
			_ = rows.Close()
		}
	}
	return g, nil
}

// Hours is a group's hourly counts over the last days (the group page's chart).
func (ix *Index) Hours(ctx context.Context, fp string, days int) (map[string]int64, error) {
	nowHour := ix.now().UnixMilli() / int64(time.Hour/time.Millisecond)
	rows, err := ix.db.QueryContext(ctx, `SELECT hour, count FROM error_hours WHERE fp = ? AND hour > ? ORDER BY hour`, fp,
		nowHour-int64(days*24))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var h, n int64
		if rows.Scan(&h, &n) == nil {
			out[time.UnixMilli(h*int64(time.Hour/time.Millisecond)).UTC().Format(time.RFC3339)] = n
		}
	}
	return out, nil
}

// Samples are a group's recent lines (a line that aged out is left out).
func (ix *Index) Samples(ctx context.Context, fp string) (Result, error) {
	var raw string
	if err := ix.db.QueryRowContext(ctx, `SELECT sample_line_ids FROM error_groups WHERE fp = ?`, fp).Scan(&raw); err != nil {
		return Result{}, err
	}
	var ids []int64
	_ = json.Unmarshal([]byte(raw), &ids)
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i] // newest first
	}
	return ix.byIDs(ctx, ids)
}

// ErrStatus is a status the index does not take.
var ErrStatus = errors.New("logindex: status must be seen, resolved or muted")

// SetStatus is an admin's change: seen, resolved (in version) or muted (until a time; zero:
// until it is resolved).
func (ix *Index) SetStatus(ctx context.Context, fp, status, version string, mutedUntil time.Time) error {
	var res sql.Result
	var err error
	switch status {
	case "seen":
		res, err = ix.db.ExecContext(ctx, `UPDATE error_groups SET status = 'seen' WHERE fp = ?`, fp)
	case "resolved":
		res, err = ix.db.ExecContext(ctx, `UPDATE error_groups SET status = 'resolved', resolved_in = ?, came_back_in = NULL,
			muted_until = NULL WHERE fp = ?`, version, fp)
	case "muted":
		var until any
		if !mutedUntil.IsZero() {
			until = mutedUntil.UnixMilli()
		}
		res, err = ix.db.ExecContext(ctx, `UPDATE error_groups SET status = 'muted', muted_until = ? WHERE fp = ?`, until, fp)
	default:
		return ErrStatus
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Notice is a group an admin should hear about: new with 3 lines in 10 minutes, or back.
type Notice struct {
	FP         string `json:"fp"`
	Title      string `json:"title"`
	Component  string `json:"component,omitempty"`
	Service    string `json:"service"`
	CameBackIn string `json:"came_back_in,omitempty"`
}

// Notices are the groups to notify about now (story 53.10 §3): new with at least 3 lines
// in the last 10 minutes, or came back; never muted; one per group a day; at most limit a
// day in all (the caller says "and N more"). Each one returned is marked notified.
func (ix *Index) Notices(ctx context.Context, limit int) ([]Notice, int, error) {
	now := ix.now().UnixMilli()
	day := now - int64(24*time.Hour/time.Millisecond)
	var sentToday int
	_ = ix.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_groups WHERE notified_at >= ?`, day).Scan(&sentToday)
	rows, err := ix.db.QueryContext(ctx, `SELECT fp, title, COALESCE(component, ''), service, COALESCE(came_back_in, '')
		FROM error_groups g WHERE status = 'new' AND (notified_at IS NULL OR notified_at < ?) AND
		(came_back_in IS NOT NULL OR (SELECT COALESCE(SUM(count), 0) FROM error_hours h WHERE h.fp = g.fp AND h.hour >= ?) >= 3)
		AND last_seen >= ? ORDER BY last_seen DESC`, day, (now-int64(10*time.Minute/time.Millisecond))/int64(time.Hour/time.Millisecond),
		now-int64(10*time.Minute/time.Millisecond))
	if err != nil {
		return nil, 0, err
	}
	var all []Notice
	for rows.Next() {
		var n Notice
		if rows.Scan(&n.FP, &n.Title, &n.Component, &n.Service, &n.CameBackIn) == nil {
			all = append(all, n)
		}
	}
	_ = rows.Close()
	room := limit - sentToday
	if room <= 0 {
		return nil, len(all), nil
	}
	send := all
	more := 0
	if len(send) > room {
		send, more = send[:room], len(send)-room
	}
	for _, n := range send {
		_, _ = ix.db.ExecContext(ctx, `UPDATE error_groups SET notified_at = ? WHERE fp = ?`, now, n.FP)
	}
	return send, more, nil
}

// sweepErrors drops groups not seen for the errors' retention, with their hours and users,
// and hours older than it.
func (ix *Index) sweepErrors(ctx context.Context, keep time.Duration) error {
	cut := ix.now().Add(-keep).UnixMilli()
	cutHour := cut / int64(time.Hour/time.Millisecond)
	for _, q := range []string{
		`DELETE FROM error_hours WHERE fp IN (SELECT fp FROM error_groups WHERE last_seen < ?)`,
		`DELETE FROM error_users WHERE fp IN (SELECT fp FROM error_groups WHERE last_seen < ?)`,
		`DELETE FROM error_groups WHERE last_seen < ?`,
	} {
		if _, err := ix.db.ExecContext(ctx, q, cut); err != nil {
			return err
		}
	}
	_, err := ix.db.ExecContext(ctx, `DELETE FROM error_hours WHERE hour < ?`, cutHour)
	return err
}

// errorRoutes serves the groups on the role's loopback listener (the API proxies them to
// admins, with the audit): GET /errors, GET /errors/{fp}, POST /errors/{fp}/status and
// POST /errors/notices (the API's notifier loop).
func (ix *Index) errorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /errors", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		q := GroupQuery{Service: v.Get("service"), Component: v.Get("component"), Q: v.Get("q"), Sort: v.Get("sort")}
		if s := v.Get("status"); s != "" {
			q.Status = strings.Split(s, ",")
		}
		if s := v.Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				writeErr(w, http.StatusBadRequest, errors.New("since"))
				return
			}
			q.Since = t
		}
		q.Limit, _ = strconv.Atoi(v.Get("limit"))
		gs, err := ix.Groups(r.Context(), q)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"groups": gs})
	})
	mux.HandleFunc("GET /errors/{fp}", func(w http.ResponseWriter, r *http.Request) {
		fp := r.PathValue("fp")
		g, err := ix.Group(r.Context(), fp, 14)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, errors.New("no such group"))
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		hours, _ := ix.Hours(r.Context(), fp, 7)
		samples, _ := ix.Samples(r.Context(), fp)
		writeJSON(w, map[string]any{"group": g, "hours": hours, "samples": samples})
	})
	mux.HandleFunc("POST /errors/{fp}/status", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Status     string `json:"status"`
			Version    string `json:"version"`
			MutedUntil string `json:"muted_until"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("body"))
			return
		}
		var until time.Time
		if body.MutedUntil != "" {
			t, err := time.Parse(time.RFC3339, body.MutedUntil)
			if err != nil {
				writeErr(w, http.StatusBadRequest, errors.New("muted_until"))
				return
			}
			until = t
		}
		err := ix.SetStatus(r.Context(), r.PathValue("fp"), body.Status, body.Version, until)
		switch {
		case errors.Is(err, ErrStatus):
			writeErr(w, http.StatusBadRequest, err)
		case errors.Is(err, sql.ErrNoRows):
			writeErr(w, http.StatusNotFound, errors.New("no such group"))
		case err != nil:
			writeErr(w, http.StatusInternalServerError, err)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /errors/notices", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 10
		}
		ns, more, err := ix.Notices(r.Context(), limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"notices": ns, "more": more})
	})
}

// sweepReports removes the problem reports older than ReportRetention and the crash
// reports' days older than ErrorRetention (the API writes both; stories 53.14, 53.15).
func (ix *Index) sweepReports() {
	now := ix.now()
	if ix.ReportRetention > 0 {
		dir := filepath.Join(ix.root, "reports")
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if _, err := strconv.Atoi(e.Name()); err != nil || !e.IsDir() {
				continue
			}
			fi, err := os.Stat(filepath.Join(dir, e.Name(), "report.json"))
			if err == nil && now.Sub(fi.ModTime()) > ix.ReportRetention {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	if ix.ErrorRetention > 0 {
		dir := filepath.Join(ix.root, "crashes")
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			day, err := time.Parse("20060102", e.Name())
			if err == nil && now.Sub(day) > ix.ErrorRetention+24*time.Hour {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
}
