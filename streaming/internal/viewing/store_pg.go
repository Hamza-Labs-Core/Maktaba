package viewing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tableRecheck: a database without tv_watch_sessions (slot 0158 not applied yet:
// the API migrates, maybe after this process started) is asked again after this.
const tableRecheck = time.Minute

// PGStore writes the viewings to Postgres: tv_watch_sessions, hdhr_tuner_leases
// and channel_runtime.viewer_count.
type PGStore struct {
	Pool *pgxpool.Pool

	mu        sync.Mutex
	hasTable  bool
	checkedAt time.Time
	salt      []byte
}

// hasViewings reports whether tv_watch_sessions exists (rechecked while it does not).
func (s *PGStore) hasViewings(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasTable || (!s.checkedAt.IsZero() && time.Since(s.checkedAt) < tableRecheck) {
		return s.hasTable
	}
	var ok bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('tv_watch_sessions') IS NOT NULL`).Scan(&ok); err == nil {
		s.hasTable, s.checkedAt = ok, time.Now()
	}
	return s.hasTable
}

// saltFor is the home's salt for the tuner clients' addresses, derived from the
// cluster stream key (tv_cluster_secret: 32 random bytes the home made at its first
// start, never shown anywhere): the same address hashes the same for the life of the
// home, and the hash says nothing without the key. nil while there is no key.
func (s *PGStore) saltFor(ctx context.Context) []byte {
	s.mu.Lock()
	salt := s.salt
	s.mu.Unlock()
	if salt != nil {
		return salt
	}
	var key []byte
	if err := s.Pool.QueryRow(ctx, `SELECT stream_key FROM tv_cluster_secret WHERE id = 1`).Scan(&key); err != nil || len(key) == 0 {
		return nil
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("maktaba tv viewer address salt"))
	salt = m.Sum(nil)
	s.mu.Lock()
	s.salt = salt
	s.mu.Unlock()
	return salt
}

// HashAddr is an address hashed with salt (hex HMAC-SHA256).
func HashAddr(salt []byte, addr string) string {
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(addr))
	return hex.EncodeToString(m.Sum(nil))
}

// Begin ends the app and tuner viewings a previous run left open (at their last
// segment, idle) and drops its tuner leases: this process holds none yet.
func (s *PGStore) Begin(ctx context.Context) error {
	if s.hasViewings(ctx) {
		if _, err := s.Pool.Exec(ctx, `
			UPDATE tv_watch_sessions
			SET ended_at = last_seen_at, end_reason = 'idle',
			    seconds = GREATEST(0, EXTRACT(EPOCH FROM (last_seen_at - started_at))::int)
			WHERE ended_at IS NULL AND surface IN ('app', 'tuner')`); err != nil {
			return err
		}
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM hdhr_tuner_leases`)
	return err
}

// TrackingOff implements Store.
func (s *PGStore) TrackingOff(ctx context.Context, users []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id FROM user_analytics_prefs
		WHERE user_id = ANY($1::uuid[]) AND NOT track_enabled`, users)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	off := map[uuid.UUID]bool{}
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		off[u] = true
	}
	return off, rows.Err()
}

// Write implements Store: one round trip. A viewing of a channel or person that no
// longer exists is skipped (the routes answer any channel id).
func (s *PGStore) Write(ctx context.Context, b Batch) error {
	var batch pgx.Batch
	if len(b.Rows) > 0 && s.hasViewings(ctx) {
		salt := s.saltFor(ctx)
		n := len(b.Rows)
		var (
			ids, chans               = make([]uuid.UUID, n), make([]uuid.UUID, n)
			users                    = make([]*uuid.UUID, n)
			surf, app, ver, plat, dv = make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
			hash, reason             = make([]string, n), make([]string, n)
			start, seen              = make([]time.Time, n), make([]time.Time, n)
			ended                    = make([]*time.Time, n)
			secs                     = make([]int32, n)
		)
		for i, r := range b.Rows {
			ids[i], chans[i], surf[i] = r.ID, r.Channel, r.Surface
			if r.User != uuid.Nil {
				u := r.User
				users[i] = &u
			}
			app[i], ver[i], plat[i], dv[i] = r.Client.App, r.Client.Version, r.Client.Platform, r.Client.Device
			if r.Surface == SurfaceTuner && r.Addr != "" && salt != nil {
				hash[i] = HashAddr(salt, r.Addr)
			}
			start[i], seen[i], reason[i], secs[i] = r.Started, r.LastSeen, r.EndReason, int32(r.Seconds()) //nolint:gosec // seconds of one viewing
			if !r.Ended.IsZero() {
				e := r.Ended
				ended[i] = &e
			}
		}
		batch.Queue(`
			INSERT INTO tv_watch_sessions AS w
				(id, channel_id, user_id, surface, client_app, client_version, platform, device_type,
				 client_addr_hash, started_at, last_seen_at, ended_at, seconds, end_reason)
			SELECT v.id, v.ch, v.usr, v.surf, NULLIF(v.app, ''), NULLIF(v.ver, ''), NULLIF(v.plat, ''),
			       NULLIF(v.dev, ''), NULLIF(v.hash, ''), v.st, v.seen, v.ended, v.secs, NULLIF(v.reason, '')
			FROM unnest($1::uuid[], $2::uuid[], $3::uuid[], $4::text[], $5::text[], $6::text[], $7::text[],
			            $8::text[], $9::text[], $10::timestamptz[], $11::timestamptz[], $12::timestamptz[],
			            $13::int[], $14::text[])
			     AS v(id, ch, usr, surf, app, ver, plat, dev, hash, st, seen, ended, secs, reason)
			WHERE EXISTS (SELECT 1 FROM channels c WHERE c.id = v.ch)
			  AND (v.usr IS NULL OR EXISTS (SELECT 1 FROM users u WHERE u.id = v.usr))
			ON CONFLICT (id) DO UPDATE SET
				last_seen_at = EXCLUDED.last_seen_at,
				seconds = EXCLUDED.seconds,
				ended_at = EXCLUDED.ended_at,
				end_reason = EXCLUDED.end_reason`,
			ids, chans, users, surf, app, ver, plat, dv, hash, start, seen, ended, secs, reason)
	}
	// the tuner page's leases: one per open tuner viewing
	var open, gone []uuid.UUID
	var openCh []uuid.UUID
	var openAddr []string
	var openStart, openSeen []time.Time
	for _, r := range b.Rows {
		if r.Surface != SurfaceTuner {
			continue
		}
		if r.Ended.IsZero() {
			open, openCh, openAddr = append(open, r.ID), append(openCh, r.Channel), append(openAddr, r.Addr)
			openStart, openSeen = append(openStart, r.Started), append(openSeen, r.LastSeen)
		} else {
			gone = append(gone, r.ID)
		}
	}
	if len(open) > 0 {
		batch.Queue(`
			INSERT INTO hdhr_tuner_leases (id, channel_id, client_addr, started_at, last_seen)
			SELECT v.id, v.ch, v.addr, v.st, v.seen
			FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::timestamptz[], $5::timestamptz[])
			     AS v(id, ch, addr, st, seen)
			WHERE EXISTS (SELECT 1 FROM channels c WHERE c.id = v.ch)
			ON CONFLICT (id) DO UPDATE SET last_seen = EXCLUDED.last_seen`,
			open, openCh, openAddr, openStart, openSeen)
	}
	if len(gone) > 0 {
		batch.Queue(`DELETE FROM hdhr_tuner_leases WHERE id = ANY($1::uuid[])`, gone)
	}
	if b.Viewers != nil {
		ids := make([]uuid.UUID, 0, len(b.Viewers))
		ns := make([]int32, 0, len(b.Viewers))
		for ch, n := range b.Viewers {
			if n > 0 {
				ids = append(ids, ch)
				ns = append(ns, int32(n)) //nolint:gosec // a viewer count
			}
		}
		batch.Queue(`
			UPDATE channel_runtime AS r SET viewer_count = v.n
			FROM unnest($1::uuid[], $2::int[]) AS v(id, n)
			WHERE r.channel_id = v.id AND r.viewer_count IS DISTINCT FROM v.n`, ids, ns)
		batch.Queue(`UPDATE channel_runtime SET viewer_count = 0
			WHERE viewer_count <> 0 AND NOT (channel_id = ANY($1::uuid[]))`, ids)
	}
	if batch.Len() == 0 {
		return nil
	}
	br := s.Pool.SendBatch(ctx, &batch)
	var errs []error
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := br.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
