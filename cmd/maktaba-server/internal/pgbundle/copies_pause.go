package pgbundle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The machines' copies of the home's database (Epic 37: logical replication, each
// machine's own replication role maktaba_rep_<id>) during an update's migrations (research
// item 5, E4 and E8). Before the migrations the database step keeps the copies out until
// the schema is settled (a table sync's walsender holds one snapshot for its whole COPY,
// which an index build waits on): pg_hba.conf rejects their roles, and their sessions are
// ended.
//
// It is pg_hba.conf, not the roles: a machine asks for its copy again and again
// (cluster_node_replica_grant, slot 0147), which sets LOGIN on its role, and no released
// home or machine can be told not to. A grant cannot undo a reject. The pause is a marker
// in the data directory that hbaConf follows, so it outlives a kill of the server; and it
// dies with the file where nothing knows the marker: every release writes pg_hba.conf at
// every start (Prepare), and one from before the marker writes it without the rejects, so a
// rollback to such a release leaves no copy paused. Each machine's slot keeps the home's
// changes meanwhile, under the home's cap (rules.cluster.replica_wal_keep_gb), and its copy
// goes on from where it stopped.

// copiesPausedMarker marks the machines' copies kept out (PauseCopies).
const copiesPausedMarker = "maktaba-copies-paused"

// replicaRoles is every machine's replication role in pg_hba.conf: a regular expression
// (PostgreSQL 16), so a role made or altered meanwhile is kept out too, and one dropped
// changes nothing. Never +maktaba_replicas: the app role, which the migrations log in as, is
// a member of that group (it made it). Neither it nor the superuser has this prefix.
const replicaRoles = "/^maktaba_rep_"

// replicaRolesSQL is the same roles in SQL.
const replicaRolesSQL = `LIKE 'maktaba\_rep\_%'`

// pausedHBA are the lines that keep the copies out, before every other (the first line
// that matches decides); "" while they are not paused.
func (b Bundle) pausedHBA() string {
	if !b.CopiesPaused() {
		return ""
	}
	return "# The machines' copies wait for the home's migrations (" + copiesPausedMarker + ").\n" +
		fmt.Sprintf("%-7s %-12s %-16s %-15s reject\n", "local", "all", replicaRoles, "") +
		fmt.Sprintf("%-7s %-12s %-16s %-15s reject\n", "host", "all", replicaRoles, "all")
}

// endCopiesSQL ends the sessions of the machines' replication roles and counts those that
// were there (a walsender takes a moment to go).
const endCopiesSQL = `SELECT count(*) FROM (SELECT pg_terminate_backend(pid) FROM pg_stat_activity
    WHERE usename ` + replicaRolesSQL + ` AND pid <> pg_backend_pid()) e`

// pauseQuery bounds one question of a pause (a server that does not answer is no reason to
// wait without end).
const pauseQuery = 15 * time.Second

// askOne asks for one value, pauseQuery at most.
func askOne(ctx context.Context, q asker, sql string) (string, error) {
	qctx, cancel := context.WithTimeout(ctx, pauseQuery)
	defer cancel()
	rows, err := q.Query(qctx, "postgres", sql)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 {
		return "", fmt.Errorf("%d rows, not 1", len(rows))
	}
	return strings.TrimSpace(rows[0]), nil
}

// CopiesPaused reports whether the machines' copies are kept out (PauseCopies).
func (b Bundle) CopiesPaused() bool {
	_, err := os.Stat(filepath.Join(b.Data, copiesPausedMarker))
	return err == nil
}

// PauseCopies keeps the machines' copies out of the running server until ResumeCopies: it
// rejects their roles in pg_hba.conf, waits until the server goes by it, then ends their
// sessions until none is left; it returns how many it ended. All of it or nothing: when it
// cannot, the copies are let in again and err says why. Safe to repeat.
func (b Bundle) PauseCopies(ctx context.Context) (ended int, err error) {
	q, err := b.NewQuerier()
	if err != nil {
		return 0, err
	}
	ver, err := askOne(ctx, q, `SELECT current_setting('server_version_num')`)
	if err != nil {
		return 0, err
	}
	if n, _ := strconv.Atoi(ver); n < 160000 {
		return 0, fmt.Errorf("PostgreSQL %s cannot reject the machines' roles by name (16 can)", ver)
	}
	before, err := askOne(ctx, q, `SELECT pg_conf_load_time()`)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(b.Data, copiesPausedMarker),
		[]byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return 0, err
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			if rerr := b.ResumeCopies(ctx); rerr != nil {
				err = errors.Join(err, fmt.Errorf("and they could not be let in again: %w", rerr))
			}
		}
	}()
	if err := b.writeHBA(); err != nil {
		return 0, fmt.Errorf("pg_hba.conf: %w", err)
	}
	if err := reloadConf(ctx, q); err != nil {
		return 0, err
	}
	// in force: every session that starts from now on was let in by the new lines
	inForce := `SELECT (pg_conf_load_time() > '` + before + `'::timestamptz)
	    AND NOT EXISTS (SELECT 1 FROM pg_hba_file_rules WHERE error IS NOT NULL)
	    AND EXISTS (SELECT 1 FROM pg_hba_file_rules WHERE auth_method = 'reject' AND '` + replicaRoles + `' = ANY (user_name))`
	if err := until(ctx, pauseQuery, func() (bool, error) {
		s, err := askOne(ctx, q, inForce)
		return s == "t", err
	}); err != nil {
		return 0, fmt.Errorf("pg_hba.conf's rejects are not in force: %w", err)
	}
	// one that was signing in meanwhile is found by the next look: none on two in a row
	quiet := 0
	if err := until(ctx, 2*pauseQuery, func() (bool, error) {
		s, err := askOne(ctx, q, endCopiesSQL)
		if err != nil {
			return false, err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return false, err
		}
		if quiet++; n > 0 {
			quiet, ended = 0, max(ended, n)
		}
		return quiet >= 2, nil
	}); err != nil {
		return ended, fmt.Errorf("the copies' sessions do not end: %w", err)
	}
	return ended, nil
}

// ResumeCopies lets the machines' copies in again. When the server cannot be told, the
// marker stays: WatchCopies, or the next start's database step, tries again.
func (b Bundle) ResumeCopies(ctx context.Context) error {
	marker := filepath.Join(b.Data, copiesPausedMarker)
	was, rerr := os.ReadFile(marker)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return rerr
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	err := b.writeHBA()
	if err == nil {
		var q Querier
		if q, err = b.NewQuerier(); err == nil {
			err = reloadConf(ctx, q)
		}
	}
	if err != nil && rerr == nil {
		if werr := os.WriteFile(marker, was, 0o600); werr != nil {
			err = errors.Join(err, werr)
		}
	}
	return err
}

func reloadConf(ctx context.Context, q asker) error {
	s, err := askOne(ctx, q, `SELECT pg_reload_conf()`)
	if err == nil && s != "t" {
		err = errors.New("the server did not reload its configuration")
	}
	return err
}

// until asks every half second until done says so, most at most.
func until(ctx context.Context, most time.Duration, done func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, most)
	defer cancel()
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// CopiesEvery is how often WatchCopies looks.
const CopiesEvery = time.Minute

// WatchCopies lets the machines' copies in again when they are still kept out with the
// schema settled (settled: the database's record says no migration, restore or held update
// is under way) and no maintenance running (Hold): a resume the server did not take, or a
// rollback whose switch back failed, never leaves them out for good.
func (b Bundle) WatchCopies(ctx context.Context, every time.Duration, settled func() bool, log io.Writer) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !b.CopiesPaused() || maintenance.Load() > 0 || !settled() {
			continue
		}
		if err := b.ResumeCopies(ctx); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(log, "[postgres] level=error the machines' copies could not be let in again (trying again): %v\n", err)
			}
			continue
		}
		fmt.Fprintln(log, "[postgres] the machines' copies may log in again")
	}
}
