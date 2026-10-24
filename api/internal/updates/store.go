package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Store is release_catalog, update_runs and update_watermark (slot 0132), on Postgres
// or SQLite.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) sqlite() bool {
	return s.DB != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", s.DB.Driver())), "sqlite")
}

// ts is a timestamp parameter: native on Postgres, the columns' text form on SQLite.
func (s *Store) ts(t time.Time) any {
	if s.sqlite() {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}

func (s *Store) tsp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return s.ts(*t)
}

func (s *Store) boolv(b bool) any {
	if s.sqlite() {
		if b {
			return 1
		}
		return 0
	}
	return b
}

// nullTime scans a native or text timestamp, NULL as nil.
type nullTime struct{ t *time.Time }

func (n *nullTime) Scan(v any) error {
	n.t = nil
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		t := x.UTC()
		n.t = &t
		return nil
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	}
	return fmt.Errorf("updates: cannot scan %T as a time", v)
}

func (n *nullTime) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			n.t = &t
			return nil
		}
	}
	return fmt.Errorf("updates: bad time %q", s)
}

// nullJSON scans a json/jsonb or text column.
type nullJSON struct{ b []byte }

func (n *nullJSON) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		n.b = nil
	case []byte:
		n.b = append([]byte(nil), x...)
	case string:
		n.b = []byte(x)
	default:
		return fmt.Errorf("updates: cannot scan %T as JSON", v)
	}
	return nil
}

// boolScan scans a boolean or SQLite integer.
type boolScan struct{ v bool }

func (b *boolScan) Scan(v any) error {
	switch x := v.(type) {
	case bool:
		b.v = x
	case int64:
		b.v = x != 0
	case nil:
		b.v = false
	default:
		return fmt.Errorf("updates: cannot scan %T as a bool", v)
	}
	return nil
}

const releaseCols = `version, tag, channel, published_at, prerelease, release_url, notes, assets, manifest,
	checksums_sha256, verify_state, verify_error, first_seen_at, verified_at, withdrawn_at, blocked_reason,
	held_at, approved_at, COALESCE(approved_by, ''), attempts`

func scanRelease(sc interface{ Scan(...any) error }) (Release, error) {
	var r Release
	var pub, first, ver, wd, held, appr nullTime
	var pre boolScan
	var assets, man, verr nullJSON
	if err := sc.Scan(&r.Version, &r.Tag, &r.Channel, &pub, &pre, &r.ReleaseURL, &r.Notes, &assets, &man,
		&r.ChecksumsSHA256, &r.VerifyState, &verr, &first, &ver, &wd, &r.BlockedReason, &held, &appr,
		&r.ApprovedBy, &r.Attempts); err != nil {
		return r, err
	}
	r.PublishedAt, r.Prerelease, r.VerifiedAt, r.WithdrawnAt, r.HeldAt, r.ApprovedAt = pub.t, pre.v, ver.t, wd.t, held.t, appr.t
	if first.t != nil {
		r.FirstSeenAt = *first.t
	}
	if len(assets.b) > 0 {
		_ = json.Unmarshal(assets.b, &r.Assets)
	}
	if len(man.b) > 0 && string(man.b) != "null" {
		var m Manifest
		if json.Unmarshal(man.b, &m) == nil {
			r.Manifest = &m
		}
	}
	if len(verr.b) > 0 && string(verr.b) != "null" {
		var e VerifyError
		if json.Unmarshal(verr.b, &e) == nil {
			r.VerifyError = &e
		}
	}
	return r, nil
}

// Catalog is every catalog entry, newest version first.
func (s *Store) Catalog(ctx context.Context) ([]Release, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+releaseCols+` FROM release_catalog`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Release
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortReleases(out)
	return out, nil
}

// Get is one catalog entry (sql.ErrNoRows when unknown).
func (s *Store) Get(ctx context.Context, version string) (Release, error) {
	return scanRelease(s.DB.QueryRowContext(ctx, `SELECT `+releaseCols+` FROM release_catalog WHERE version = $1`,
		NormalizeVersion(version)))
}

// Seen records a release from GitHub: a new entry (true) or the existing one's GitHub
// fields refreshed. Verification state is not touched.
func (s *Store) Seen(ctx context.Context, r Release) (bool, error) {
	assets, _ := json.Marshal(nonNilAssets(r.Assets))
	res, err := s.DB.ExecContext(ctx, `INSERT INTO release_catalog (version, tag, channel, published_at, prerelease,
		release_url, notes, assets, first_seen_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (version) DO NOTHING`, r.Version, r.Tag, r.Channel, s.tsp(r.PublishedAt), s.boolv(r.Prerelease),
		r.ReleaseURL, r.Notes, string(assets), s.ts(s.now()))
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE release_catalog SET tag = $2, channel = $3, published_at = $4,
		prerelease = $5, release_url = $6, notes = $7, assets = $8 WHERE version = $1`, r.Version, r.Tag, r.Channel,
		s.tsp(r.PublishedAt), s.boolv(r.Prerelease), r.ReleaseURL, r.Notes, string(assets))
	return false, err
}

// SetVerified stores a verified manifest.
func (s *Store) SetVerified(ctx context.Context, version string, v Verified) error {
	m := v.Manifest
	m.SignedAt = v.SignedAt // the verified signing time, never one release.json names
	if !m.SignedAt.IsZero() {
		m.SignedAt = m.SignedAt.UTC()
	}
	man, _ := json.Marshal(m)
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET verify_state = 'verified', verify_error = NULL,
		manifest = $2, checksums_sha256 = $3, verified_at = $4 WHERE version = $1`,
		version, string(man), v.ChecksumsSHA256, s.ts(s.now()))
	return err
}

// SetVerifyError records a failure: state failed for an incident, unverified otherwise.
func (s *Store) SetVerifyError(ctx context.Context, version string, ve *VerifyError, checksums string) error {
	state := VerifyUnverified
	if ve.Incident() {
		state = VerifyFailed
	}
	b, _ := json.Marshal(ve)
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET verify_state = $2, verify_error = $3,
		checksums_sha256 = CASE WHEN $4 = '' THEN checksums_sha256 ELSE $4 END WHERE version = $1`,
		version, state, string(b), checksums)
	return err
}

// ResetForReverify forgets a verification (a re-tagged release, story 35.3 EC4).
func (s *Store) ResetForReverify(ctx context.Context, version string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET verify_state = 'unverified', verify_error = NULL,
		manifest = NULL, verified_at = NULL, held_at = NULL, approved_at = NULL, approved_by = NULL WHERE version = $1`, version)
	return err
}

// SetWithdrawn marks a release withdrawn (story 35.3 AC6); reason goes to blocked_reason
// only when it was empty.
func (s *Store) SetWithdrawn(ctx context.Context, version, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET withdrawn_at = $2,
		blocked_reason = CASE WHEN blocked_reason = '' THEN $3 ELSE blocked_reason END WHERE version = $1`,
		version, s.ts(s.now()), reason)
	return err
}

// SetHeld records the first time a contract release was held (true when this call set
// it, so it is notified once).
func (s *Store) SetHeld(ctx context.Context, version string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET held_at = $2 WHERE version = $1 AND held_at IS NULL`,
		version, s.ts(s.now()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Approve records an admin's approval of a held release.
func (s *Store) Approve(ctx context.Context, version, userID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET approved_at = $2, approved_by = $3 WHERE version = $1`,
		version, s.ts(s.now()), userID)
	return err
}

// Watermark is the cluster's highest committed version ("" before the first).
func (s *Store) Watermark(ctx context.Context) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT highest_installed FROM update_watermark WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// RaiseWatermark sets the watermark to v when v is higher (it only rises).
func (s *Store) RaiseWatermark(ctx context.Context, v string) error {
	cur, err := s.Watermark(ctx)
	if err != nil {
		return err
	}
	v = NormalizeVersion(v)
	if !ValidVersion(v) || (cur != "" && CompareVersions(v, cur) <= 0) {
		return nil
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO update_watermark (id, highest_installed, set_at) VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET highest_installed = EXCLUDED.highest_installed, set_at = EXCLUDED.set_at`,
		v, s.ts(s.now()))
	return err
}

// Run is one update_runs row.
type Run struct {
	ID          string          `json:"id"`
	Version     string          `json:"version"`
	FromVersion string          `json:"from_version"`
	Trigger     string          `json:"trigger"`
	State       string          `json:"state"`
	Step        string          `json:"step"`
	StartedAt   time.Time       `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	// Backup is the dump the home's update took before its migrations: {path, bytes}
	Backup json.RawMessage `json:"backup,omitempty"`
}

// Triggers.
const (
	TriggerAuto    = "auto"
	TriggerManual  = "manual"
	TriggerCatchUp = "catch_up"
	TriggerRetry   = "retry"
)

// ErrRunActive: a run is already active (one at a time, design §5.1).
var ErrRunActive = errors.New("update_run_active")

// InsertRun inserts a planned run; ErrRunActive when one is active.
func (s *Store) InsertRun(ctx context.Context, version, from, trigger, actor string) (Run, error) {
	return s.insertRun(ctx, version, from, trigger, actor, nil)
}

func (s *Store) insertRun(ctx context.Context, version, from, trigger, actor string, d *RunDetail) (Run, error) {
	if a, err := s.ActiveRun(ctx); err != nil {
		return Run{}, err
	} else if a != nil {
		return Run{}, ErrRunActive
	}
	r := Run{ID: uuid.NewString(), Version: version, FromVersion: from, Trigger: trigger, State: "planned",
		StartedAt: s.now(), ActorUserID: actor}
	var act any
	det := "{}"
	if actor != "" {
		act = actor
	}
	if d != nil {
		b, err := json.Marshal(d)
		if err != nil {
			return Run{}, err
		}
		det = string(b)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO update_runs (id, version, from_version, trigger, state, started_at,
		actor_user_id, detail) VALUES ($1, $2, $3, $4, 'planned', $5, $6, $7)`, r.ID, version, from, trigger,
		s.ts(r.StartedAt), act, det); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(err.Error(), "update_runs_one_active_idx") {
			return Run{}, ErrRunActive
		}
		return Run{}, err
	}
	return r, nil
}

// FinishRun ends a run in state with an error document.
func (s *Store) FinishRun(ctx context.Context, id, state string, errDoc map[string]any) error {
	var e any
	if errDoc != nil {
		b, _ := json.Marshal(errDoc)
		e = string(b)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE update_runs SET state = $2, error = $3, finished_at = $4 WHERE id = $1`,
		id, state, e, s.ts(s.now()))
	return err
}

const runCols = `id, version, from_version, trigger, state, step, started_at, finished_at, error, COALESCE(actor_user_id, ''), backup`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var started, fin nullTime
	var e, backup nullJSON
	if err := sc.Scan(&r.ID, &r.Version, &r.FromVersion, &r.Trigger, &r.State, &r.Step, &started, &fin, &e,
		&r.ActorUserID, &backup); err != nil {
		return r, err
	}
	if len(backup.b) > 0 {
		r.Backup = json.RawMessage(backup.b)
	}
	if started.t != nil {
		r.StartedAt = *started.t
	}
	r.FinishedAt = fin.t
	if len(e.b) > 0 {
		r.Error = json.RawMessage(e.b)
	}
	return r, nil
}

// ActiveRun is the run without finished_at, if any.
func (s *Store) ActiveRun(ctx context.Context) (*Run, error) {
	r, err := scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM update_runs WHERE finished_at IS NULL
		ORDER BY started_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Runs is the history, newest first.
func (s *Store) Runs(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+runCols+` FROM update_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Audit writes an update action to audit_log (category update, README R9.3).
// Best-effort: an audit_log without the 0054 columns falls back to the base columns.
func (s *Store) Audit(ctx context.Context, actor, source, action, target string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	b, _ := json.Marshal(payload)
	var a any
	if _, err := uuid.Parse(actor); err == nil {
		a = actor
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id,
		target_kind, actor_source, payload) VALUES ('update', $1, $2, $3, 'release', $4, $5)`,
		action, a, target, source, string(b)); err != nil {
		_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
			VALUES ('update', $1, $2, $3, $4)`, action, a, target, string(b))
	}
}

// State is the poller's persisted state (app_settings "updates.state"): when the last
// successful check was (the 14-day freeze hint, story 35.3 AC10).
type State struct {
	LastCheckOK  *time.Time `json:"last_check_ok,omitempty"`
	LastCheckErr string     `json:"last_check_error,omitempty"`
	LastCheckAt  *time.Time `json:"last_check_at,omitempty"`
	// Since is the first check this home made (the stale hint's start when no check
	// ever succeeded).
	Since *time.Time `json:"since,omitempty"`
}

const stateKey = "updates.state"

// LoadState reads the poller state (zero when none).
func (s *Store) LoadState(ctx context.Context) State {
	var v string
	var st State
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, stateKey).Scan(&v); err == nil {
		_ = json.Unmarshal([]byte(v), &st)
	}
	return st
}

// SaveState writes the poller state (best-effort).
func (s *Store) SaveState(ctx context.Context, st State) {
	b, _ := json.Marshal(st)
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		stateKey, string(b), s.ts(s.now()))
}

func nonNilAssets(a []Asset) []Asset {
	if a == nil {
		return []Asset{}
	}
	return a
}

func sortReleases(rs []Release) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && CompareVersions(rs[j].Version, rs[j-1].Version) > 0; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}
