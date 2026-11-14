package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

// Leader lock ids. The migrator holds 8472612 (cloud/migrations/README.md); the worker's jobs
// run only on the node holding WorkerLockID (Epic 50 story 50.17 §2).
const WorkerLockID int64 = 8472613

// Leader runs work on one node of several: the one holding a Postgres session advisory lock,
// pg_try_advisory_lock(LockID), on a connection of its own. Every node runs a Leader; each
// tries for the lock every Every. If the holder's connection drops, Postgres releases the
// lock and another node takes it at its next try. The holder renews its row in cloud_leaders
// through the lock's connection every Every; a row whose heartbeat is older than StaleAfter
// names a hung holder, and the node that finds it ends that backend (pg_terminate_backend),
// which frees the lock for the next try.
//
// The lock is a session lock, so the connection must be a direct one: never through
// PgBouncer's transaction pool (story 50.18).
type Leader struct {
	DB         *sql.DB
	LockID     int64
	Node       string        // this node's name, shown in cloud_leaders
	Every      time.Duration // the try and heartbeat period; 15 s when zero
	StaleAfter time.Duration // a heartbeat this old is a hung holder; 60 s when zero
	Logger     *slog.Logger

	leading atomic.Bool
}

// Leading reports whether this node holds the lock now (the worker_leader metric).
func (l *Leader) Leading() bool { return l.leading.Load() }

func (l *Leader) every() time.Duration {
	if l.Every > 0 {
		return l.Every
	}
	return 15 * time.Second
}

func (l *Leader) staleAfter() time.Duration {
	if l.StaleAfter > 0 {
		return l.StaleAfter
	}
	return 60 * time.Second
}

func (l *Leader) logger() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

// Run tries for the lock at once and then every Every until ctx ends. While this node holds
// it, lead runs with a context that ends when the lock is lost or ctx ends; Run waits for lead
// to return before it tries again, so lead never runs twice on one node. lead's work must be
// idempotent: a node can lose the lock part way through (its connection dropped), and the
// next holder runs the same work again.
func (l *Leader) Run(ctx context.Context, lead func(context.Context)) {
	t := time.NewTicker(l.every())
	defer t.Stop()
	for {
		l.attempt(ctx, lead)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// attempt takes a connection, tries for the lock, and leads until the lock is lost.
func (l *Leader) attempt(ctx context.Context, lead func(context.Context)) {
	tctx, cancel := context.WithTimeout(ctx, l.every())
	conn, err := l.DB.Conn(tctx)
	if err != nil {
		cancel()
		if ctx.Err() == nil {
			l.logger().Warn("leader: no database connection", "lock", l.LockID, "err", err)
		}
		return
	}
	var got bool
	err = conn.QueryRowContext(tctx, `SELECT pg_try_advisory_lock($1)`, l.LockID).Scan(&got)
	if err == nil && !got {
		l.endStaleHolder(tctx, conn)
	}
	cancel()
	if err != nil || !got {
		// an error may have left the lock taken on a connection we can't trust: drop it
		if err != nil {
			discard(conn)
			if ctx.Err() == nil {
				l.logger().Warn("leader: the lock try failed", "lock", l.LockID, "err", err)
			}
			return
		}
		_ = conn.Close()
		return
	}
	// the lock's connection never goes back to the pool: closing it ends the session, which
	// releases the lock, whatever happens below
	defer discard(conn)
	if err := l.heartbeat(ctx, conn, true); err != nil {
		l.logger().Warn("leader: took the lock but could not record it", "lock", l.LockID, "err", err)
		return
	}
	l.leading.Store(true)
	l.logger().Info("leader: this node leads", "lock", l.LockID, "node", l.Node, "event", "leader_acquired")
	leadCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lead(leadCtx)
	}()

	t := time.NewTicker(l.every())
	defer t.Stop()
	reason := "stopping"
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-done:
			reason = "its work returned"
			break loop
		case <-t.C:
			if err := l.heartbeat(ctx, conn, false); err != nil {
				if ctx.Err() != nil {
					break loop
				}
				reason = "lost the lock: " + err.Error()
				break loop
			}
		}
	}
	stop()
	<-done
	l.leading.Store(false)
	l.logger().Info("leader: this node no longer leads", "lock", l.LockID, "node", l.Node, "reason", reason, "event", "leader_released")
}

// errNotHolder is a heartbeat whose row names another backend: the lock was taken from us.
var errNotHolder = errors.New("cloud_leaders names another holder")

// heartbeat records this node as the holder (first) or renews its heartbeat, through the
// lock's own connection: a dead connection fails here, and the node stops leading.
func (l *Leader) heartbeat(ctx context.Context, conn *sql.Conn, first bool) error {
	ctx, cancel := context.WithTimeout(ctx, l.every())
	defer cancel()
	if first {
		_, err := conn.ExecContext(ctx, `
            INSERT INTO cloud_leaders (lock_id, node, backend_pid, since, heartbeat_at)
            VALUES ($1, $2, pg_backend_pid(), now(), now())
            ON CONFLICT (lock_id) DO UPDATE SET node = EXCLUDED.node, backend_pid = EXCLUDED.backend_pid,
                since = now(), heartbeat_at = now()`, l.LockID, l.Node)
		return err
	}
	res, err := conn.ExecContext(ctx, `UPDATE cloud_leaders SET heartbeat_at = now()
        WHERE lock_id = $1 AND backend_pid = pg_backend_pid()`, l.LockID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotHolder
	}
	return nil
}

// endStaleHolder ends the backend holding the lock when its heartbeat is older than
// StaleAfter: a hung process keeps its connection, and with it the lock (story 50.17, edge
// cases). The lock is free at the next try.
func (l *Leader) endStaleHolder(ctx context.Context, conn *sql.Conn) {
	stale := l.staleAfter().Seconds()
	var pid int
	var node string
	err := conn.QueryRowContext(ctx, `
        SELECT k.pid, COALESCE(c.node, '')
          FROM pg_locks k
          JOIN pg_stat_activity a ON a.pid = k.pid
          LEFT JOIN cloud_leaders c ON c.lock_id = $1
         WHERE k.locktype = 'advisory' AND k.granted AND k.objsubid = 1
           AND ((k.classid::bigint << 32) | k.objid::bigint) = $1
           AND CASE WHEN c.backend_pid = k.pid
                    THEN c.heartbeat_at < now() - make_interval(secs => $2)
                    -- no row names the holder yet: only one that has held its session that long
                    ELSE a.backend_start < now() - make_interval(secs => $2)
                         AND (c.heartbeat_at IS NULL OR c.heartbeat_at < now() - make_interval(secs => $2))
               END
         LIMIT 1`, l.LockID, stale).Scan(&pid, &node)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		l.logger().Warn("leader: could not check the holder's heartbeat", "lock", l.LockID, "err", err)
		return
	}
	var ended bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&ended); err != nil {
		l.logger().Warn("leader: could not end the hung holder", "lock", l.LockID, "pid", pid, "err", err)
		return
	}
	l.logger().Warn("leader: ended a holder whose heartbeat stopped", "lock", l.LockID, "holder", node,
		"pid", pid, "ended", ended, "event", "leader_holder_ended")
}

// discard closes conn's session instead of returning it to the pool.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
