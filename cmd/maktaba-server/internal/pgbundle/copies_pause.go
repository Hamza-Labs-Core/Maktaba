package pgbundle

import (
	"context"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

// The machines' copies of the home's database (Epic 37: logical replication, each
// machine's own replication role a member of ReplicaGroup) during an update's
// migrations (research item 5, E4 and E8). Before the migrations the database step waits
// for every table sync to end (a machine's first copy of a table, or one made anew: its
// walsender holds one snapshot for the whole COPY, which an index build waits on, and
// ending it makes the machine copy the table again), then stops the copies from logging
// in (NOLOGIN on each machine's replication role) and ends their walsenders, so nothing
// of theirs runs or comes straight back while the schema changes; after the migrations
// it lets them log in again. Each machine's slot keeps the home's changes meanwhile,
// under the home's cap (rules.cluster.replica_wal_keep_gb), and its copy goes on from
// where it stopped.

// TableSyncsSQL counts the table syncs streaming from the app database: their slots are
// pg_<subscription>_sync_<table>_<system>.
const TableSyncsSQL = `SELECT count(*) FROM pg_replication_slots
    WHERE slot_type = 'logical' AND database = '` + AppDatabase + `'
      AND slot_name ~ '^pg_[0-9]+_sync_[0-9]+_[0-9]+$'`

// ReplicaLoginsSQL selects the machines' replication roles that may log in now: the
// members of ReplicaGroup named maktaba_rep_<machine> (slot 0147's
// cluster_node_replica_grant makes them). Never the app role, nor a superuser: the app
// role is a member of the group too (it made it), and the migrations log in as it.
const ReplicaLoginsSQL = `SELECT m.rolname FROM pg_roles m
    JOIN pg_auth_members am ON am.member = m.oid
    JOIN pg_roles g ON g.oid = am.roleid AND g.rolname = '` + ReplicaGroup + `'
    WHERE m.rolcanlogin AND NOT m.rolsuper AND m.rolname LIKE 'maktaba\_rep\_%'
      AND m.rolname NOT IN ('` + AppRole + `', '` + SuperUser + `') AND m.rolname <> current_user
    ORDER BY 1`

// PauseCopiesSQL stops roles from logging in (each machine's replication role).
func PauseCopiesSQL(roles []string) string {
	var s strings.Builder
	for _, r := range roles {
		s.WriteString("ALTER ROLE " + pq.QuoteIdentifier(r) + " NOLOGIN;\n")
	}
	return s.String()
}

// EndCopiesSQL ends the sessions of roles (their walsenders) and counts them.
func EndCopiesSQL(roles []string) string {
	q := make([]string, len(roles))
	for i, r := range roles {
		q[i] = pq.QuoteLiteral(r)
	}
	return `SELECT count(*) FROM (SELECT pg_terminate_backend(pid) AS ended FROM pg_stat_activity
    WHERE usename IN (` + strings.Join(q, ", ") + `) AND pid <> pg_backend_pid()) e WHERE e.ended`
}

// ResumeCopiesSQL lets roles log in again.
func ResumeCopiesSQL(roles []string) string {
	var s strings.Builder
	for _, r := range roles {
		s.WriteString("ALTER ROLE " + pq.QuoteIdentifier(r) + " LOGIN;\n")
	}
	return s.String()
}

// TableSyncs counts the table syncs streaming from the app database (TableSyncsSQL).
func (b Bundle) TableSyncs(ctx context.Context) (int, error) {
	q, err := b.NewQuerier()
	if err != nil {
		return 0, err
	}
	rows, err := q.Query(ctx, "postgres", TableSyncsSQL)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(rows[0]))
}

// ReplicaLogins is the machines' replication roles that may log in now (ReplicaLoginsSQL).
func (b Bundle) ReplicaLogins(ctx context.Context) ([]string, error) {
	q, err := b.NewQuerier()
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, "postgres", ReplicaLoginsSQL)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// PauseCopies stops roles from logging in and ends their sessions; it returns how many
// it ended.
func (b Bundle) PauseCopies(ctx context.Context, roles []string) (int, error) {
	if len(roles) == 0 {
		return 0, nil
	}
	q, err := b.NewQuerier()
	if err != nil {
		return 0, err
	}
	if err := q.Exec(ctx, "postgres", PauseCopiesSQL(roles)); err != nil {
		return 0, err
	}
	rows, err := q.Query(ctx, "postgres", EndCopiesSQL(roles))
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(rows[0]))
}

// ResumeCopies lets roles log in again.
func (b Bundle) ResumeCopies(ctx context.Context, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	q, err := b.NewQuerier()
	if err != nil {
		return err
	}
	return q.Exec(ctx, "postgres", ResumeCopiesSQL(roles))
}
