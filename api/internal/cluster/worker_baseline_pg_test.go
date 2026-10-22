package cluster

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The machines' group made late (slot 0147: a home whose migrations ran without the
// right to create roles, as the Postgres bundled with Maktaba.app did) ends up with
// exactly the privileges a home that had it from slot 0103 on gives it.
//
// Roles are a server's, privileges a database's: on a server where the group does not
// exist yet, one database is migrated by a user who cannot create roles (no group, the
// grants of every slot skipped), then that user may and slot 0147 runs; another
// database is migrated by the superuser the normal way. Their grants must be the same.
func TestWorkerGroupMadeLateGetsTheSamePrivileges(t *testing.T) {
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() }) // after the clean-up below (last in, first out)
	var exists bool
	if err := admin.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')`).Scan(&exists); err != nil {
		t.Skipf("scratch Postgres unreachable: %v", err)
	}
	if exists {
		t.Skip("needs a Postgres server where maktaba_worker does not exist yet (a fresh one)")
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	plain, late, early := "late_owner_"+suffix, "t_late_"+suffix, "t_early_"+suffix
	exec := func(db *sql.DB, q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(admin, `CREATE ROLE `+plain+` LOGIN PASSWORD 'late-pass'`)
	exec(admin, `CREATE DATABASE `+late+` OWNER `+plain)
	exec(admin, `CREATE DATABASE `+early)
	t.Cleanup(func() {
		for _, d := range []string{late, early} {
			_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, d)
			if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + d); err != nil {
				t.Logf("drop %s: %v", d, err)
			}
		}
		for _, r := range []string{plain, "maktaba_worker", "maktaba_replicas"} {
			if _, err := admin.Exec(`DROP ROLE IF EXISTS ` + r); err != nil {
				t.Logf("drop role %s: %v", r, err)
			}
		}
	})
	// the extensions the migrations use come from a superuser (Maktaba.app's bootstrap)
	for _, d := range []string{late, early} {
		db, err := sql.Open("postgres", clustertest.DSN(t, base, d, "", ""))
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range []string{"pg_trgm", "unaccent", "pgcrypto"} {
			_, _ = db.Exec(`CREATE EXTENSION IF NOT EXISTS ` + x)
		}
		_ = db.Close()
	}

	lateDB, err := sql.Open("postgres", clustertest.DSN(t, base, late, plain, "late-pass"))
	if err != nil {
		t.Fatal(err)
	}
	defer lateDB.Close()
	clustertest.MigrateTo(t, lateDB, 146)
	exec(lateDB, `SELECT 1`)
	var made bool
	_ = admin.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')`).Scan(&made)
	if made {
		t.Fatal("a user who cannot create roles made the group")
	}
	exec(admin, `ALTER ROLE `+plain+` CREATEROLE`)
	clustertest.MigrateTo(t, lateDB, 0) // slot 0147

	earlyDB, err := sql.Open("postgres", clustertest.DSN(t, base, early, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer earlyDB.Close()
	clustertest.MigrateTo(t, earlyDB, 0)

	got, want := workerPrivileges(t, lateDB), workerPrivileges(t, earlyDB)
	if len(want) < 50 {
		t.Fatalf("the reference has only %d privileges: %v", len(want), want)
	}
	if missing, extra := diff(want, got); len(missing)+len(extra) > 0 {
		t.Fatalf("the late group differs:\n missing %v\n extra %v", missing, extra)
	}
}

// workerPrivileges lists what maktaba_worker may do in db: on every table, sequence
// and function of public, the schema, the database, and the default privileges.
func workerPrivileges(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	add := func(q string) {
		t.Helper()
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
		add(fmt.Sprintf(`SELECT 'table %s ' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relkind IN ('r', 'v', 'm', 'p')
			  AND has_table_privilege('maktaba_worker', c.oid, %s)`, p, pq.QuoteLiteral(p)))
	}
	for _, p := range []string{"USAGE", "SELECT", "UPDATE"} {
		add(fmt.Sprintf(`SELECT 'sequence %s ' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relkind = 'S'
			  AND CASE WHEN c.relkind = 'S' THEN has_sequence_privilege('maktaba_worker', c.oid, %s) END`, p, pq.QuoteLiteral(p)))
	}
	add(`SELECT 'function ' || p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND has_function_privilege('maktaba_worker', p.oid, 'EXECUTE')
		  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`)
	add(`SELECT 'schema usage' WHERE has_schema_privilege('maktaba_worker', 'public', 'USAGE')`)
	add(`SELECT 'database connect' WHERE has_database_privilege('maktaba_worker', current_database(), 'CONNECT')`)
	add(`SELECT 'default ' || d.defaclobjtype::text || ' ' || a.privilege_type FROM pg_default_acl d
		CROSS JOIN LATERAL aclexplode(d.defaclacl) a
		WHERE a.grantee = 'maktaba_worker'::regrole`)
	sort.Strings(out)
	return out
}

func diff(want, got []string) (missing, extra []string) {
	w, g := map[string]bool{}, map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	for _, s := range got {
		g[s] = true
		if !w[s] {
			extra = append(extra, s)
		}
	}
	for _, s := range want {
		if !g[s] {
			missing = append(missing, s)
		}
	}
	return missing, extra
}
