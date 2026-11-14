package broadcast

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RevocationStore keeps the kill switch's entries, the list's version and the audit of every
// change (migration 00500022, story 50.25 §4). Each add and lift moves the version and writes
// its audit row in one transaction.
type RevocationStore struct {
	DB *sql.DB
}

// ErrRevocationNotFound is an entry that does not exist or was lifted already.
var ErrRevocationNotFound = errors.New("broadcast: revocation not found or already lifted")

// Actor is the staff account that changes the list.
type Actor struct {
	ID    string
	Email string
}

// RevocationRow is one entry as the admin shows it.
type RevocationRow struct {
	Entry
	Note    string     `json:"note"`
	AddedBy string     `json:"added_by"`
	AddedAt time.Time  `json:"added_at"`
	Lifted  *time.Time `json:"lifted_at,omitempty"`
}

// RevocationAudit is one audit row.
type RevocationAudit struct {
	Revocation string    `json:"revocation"`
	Action     string    `json:"action"`
	Version    int64     `json:"version"`
	Actor      string    `json:"actor"`
	Note       string    `json:"note"`
	At         time.Time `json:"at"`
}

// EntryID is an entry's id in the list: "r-<row id>".
func EntryID(row int64) string { return "r-" + strconv.FormatInt(row, 10) }

// ParseEntryID is EntryID's inverse; false for anything else.
func ParseEntryID(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(s, "r-"), 10, 64)
	return n, err == nil && n > 0 && strings.HasPrefix(s, "r-")
}

func (s *RevocationStore) bump(ctx context.Context, tx *sql.Tx) (int64, error) {
	var v int64
	err := tx.QueryRowContext(ctx, `UPDATE broadcast_revocation_list SET version = version + 1, changed_at = now()
        WHERE singleton RETURNING version`).Scan(&v)
	return v, err
}

// Add records a new entry (validated first) and returns it with the list's new version.
func (s *RevocationStore) Add(ctx context.Context, e Entry, note string, by Actor) (RevocationRow, int64, error) {
	if err := e.Validate(); err != nil {
		return RevocationRow{}, 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return RevocationRow{}, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	var at time.Time
	if err := tx.QueryRowContext(ctx, `
        INSERT INTO broadcast_revocations (kind, ref, action, reason, note, until_at, added_by)
        VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, added_at`,
		e.Kind, e.Ref, e.Action, e.Reason, note, e.Until, by.ID).Scan(&id, &at); err != nil {
		return RevocationRow{}, 0, err
	}
	v, err := s.bump(ctx, tx)
	if err != nil {
		return RevocationRow{}, 0, err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO broadcast_revocation_audit (revocation_id, action, version, actor_id, actor_email, note)
        VALUES ($1, 'add', $2, $3, $4, $5)`, id, v, by.ID, by.Email, note); err != nil {
		return RevocationRow{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return RevocationRow{}, 0, err
	}
	e.ID, e.Since = EntryID(id), at.UTC()
	return RevocationRow{Entry: e, Note: note, AddedBy: by.Email, AddedAt: at.UTC()}, v, nil
}

// Lift takes an entry off the list and returns the list's new version.
func (s *RevocationStore) Lift(ctx context.Context, row int64, note string, by Actor) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE broadcast_revocations SET lifted_at = now(), lifted_by = $2, lift_note = $3
        WHERE id = $1 AND lifted_at IS NULL`, row, by.ID, note)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrRevocationNotFound
	}
	v, err := s.bump(ctx, tx)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO broadcast_revocation_audit (revocation_id, action, version, actor_id, actor_email, note)
        VALUES ($1, 'lift', $2, $3, $4, $5)`, row, v, by.ID, by.Email, note); err != nil {
		return 0, err
	}
	return v, tx.Commit()
}

// Current is the list's version and its entries not lifted (a programme past its until is
// left out at signing).
func (s *RevocationStore) Current(ctx context.Context) (int64, []Entry, error) {
	rows, err := s.rows(ctx, true)
	if err != nil {
		return 0, nil, err
	}
	var v int64
	if err := s.DB.QueryRowContext(ctx, `SELECT version FROM broadcast_revocation_list WHERE singleton`).Scan(&v); err != nil {
		return 0, nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Entry)
	}
	return v, out, nil
}

func (s *RevocationStore) rows(ctx context.Context, liveOnly bool) ([]RevocationRow, error) {
	q := `SELECT r.id, r.kind, r.ref, r.action, r.reason, r.note, r.until_at, r.added_at, r.lifted_at,
	             COALESCE((SELECT a.actor_email FROM broadcast_revocation_audit a
	                        WHERE a.revocation_id = r.id AND a.action = 'add' LIMIT 1), r.added_by)
	        FROM broadcast_revocations r`
	if liveOnly {
		q += ` WHERE r.lifted_at IS NULL`
	} else {
		q += ` WHERE r.lifted_at IS NULL OR r.lifted_at > now() - interval '30 days'`
	}
	q += ` ORDER BY r.id`
	rs, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []RevocationRow
	for rs.Next() {
		var r RevocationRow
		var id int64
		var until, lifted sql.NullTime
		if err := rs.Scan(&id, &r.Kind, &r.Ref, &r.Action, &r.Reason, &r.Note, &until, &r.AddedAt, &lifted, &r.AddedBy); err != nil {
			return nil, err
		}
		r.ID, r.Since, r.AddedAt = EntryID(id), r.AddedAt.UTC(), r.AddedAt.UTC()
		if until.Valid {
			u := until.Time.UTC()
			r.Until = &u
		}
		if lifted.Valid {
			l := lifted.Time.UTC()
			r.Lifted = &l
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// Overview is the admin's view: the version, when it last changed, the entries (live, and
// those lifted in the last 30 days) and the newest 100 audit rows.
func (s *RevocationStore) Overview(ctx context.Context) (version int64, changed time.Time, rows []RevocationRow, audit []RevocationAudit, err error) {
	if err = s.DB.QueryRowContext(ctx, `SELECT version, changed_at FROM broadcast_revocation_list WHERE singleton`).Scan(&version, &changed); err != nil {
		return
	}
	if rows, err = s.rows(ctx, false); err != nil {
		return
	}
	rs, err := s.DB.QueryContext(ctx, `SELECT revocation_id, action, version, COALESCE(NULLIF(actor_email, ''), actor_id), note, at
        FROM broadcast_revocation_audit ORDER BY at DESC, id DESC LIMIT 100`)
	if err != nil {
		return
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var a RevocationAudit
		var id int64
		if err = rs.Scan(&id, &a.Action, &a.Version, &a.Actor, &a.Note, &a.At); err != nil {
			return
		}
		a.Revocation, a.At = EntryID(id), a.At.UTC()
		audit = append(audit, a)
	}
	return version, changed.UTC(), rows, audit, rs.Err()
}

// RevocationSource is what signing reads: the version and the entries not lifted
// (RevocationStore; a fake in tests).
type RevocationSource interface {
	Current(ctx context.Context) (int64, []Entry, error)
}

// Revocations signs the current list and pushes it to the edge (story 50.25 §2).
type Revocations struct {
	Store RevocationSource
	Key   *Key
	// ControlURL is the edge's PUT /v1/control/revocations ("" pushes nothing; the edge pulls).
	ControlURL string
	HTTP       *http.Client
	Logger     *slog.Logger
	Now        func() time.Time
}

func (r *Revocations) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// ErrNoKey is the broadcast signing key missing: the list can't be signed.
var ErrNoKey = errors.New("broadcast: no broadcast signing key")

// Signed is the current list, signed now (the edge's copy: every entry).
func (r *Revocations) Signed(ctx context.Context) (string, RevocationList, error) {
	if r.Key == nil {
		return "", RevocationList{}, ErrNoKey
	}
	v, entries, err := r.Store.Current(ctx)
	if err != nil {
		return "", RevocationList{}, err
	}
	return SignRevocations(*r.Key, v, entries, r.now())
}

// SignedForHome is a home's copy of the current list, signed now under RevocationsHomeType:
// its own entries and the opaque ones (ForHome), never another home's server id, another
// account's id or a reason given about them.
func (r *Revocations) SignedForHome(ctx context.Context, serverID, owner string) (string, RevocationList, error) {
	if r.Key == nil {
		return "", RevocationList{}, ErrNoKey
	}
	v, entries, err := r.Store.Current(ctx)
	if err != nil {
		return "", RevocationList{}, err
	}
	keep := ForHome(serverID, owner)
	kept := entries[:0:0]
	for _, e := range entries {
		if keep(e) {
			kept = append(kept, e)
		}
	}
	return SignHomeRevocations(*r.Key, v, kept, r.now())
}

// ForHome is what a home's copy keeps: global entries, channel and programme entries (their
// ref is the edge's opaque channel id, which says nothing of the home), and the home and
// publisher entries naming this home or its owner.
func ForHome(serverID, owner string) func(Entry) bool {
	return func(e Entry) bool {
		switch e.Kind {
		case KindGlobal, KindChannel, KindProgramme:
			return true
		case KindHome:
			return e.Ref == serverID
		case KindPublisher:
			return owner != "" && e.Ref == owner
		}
		return false
	}
}

// Push sends the current list to the edge, three tries. A failed push is caught by the edge's
// pull every 60 s.
func (r *Revocations) Push(ctx context.Context) error {
	if r.ControlURL == "" || r.Key == nil {
		return nil
	}
	text, l, err := r.Signed(ctx)
	if err != nil {
		return err
	}
	cl := r.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 10 * time.Second}
	}
	var last error
	for try := 0; try < 3; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(try) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, r.ControlURL, bytes.NewReader([]byte(text)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "text/plain")
		resp, err := cl.Do(req)
		if err != nil {
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
		// 409: the edge holds a newer list already (a later push won the race)
		if resp.StatusCode < 300 || resp.StatusCode == http.StatusConflict {
			if r.Logger != nil {
				r.Logger.Info("broadcast: revocation list pushed", "version", l.Version, "entries", len(l.Entries), "status", resp.StatusCode)
			}
			return nil
		}
		last = fmt.Errorf("the edge answered %d", resp.StatusCode)
	}
	if r.Logger != nil {
		r.Logger.Warn("broadcast: revocation push failed; the edge pulls within 60 s", "version", l.Version, "err", last)
	}
	return last
}
