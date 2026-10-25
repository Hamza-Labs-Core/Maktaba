package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The group EnsureWorkerGroup makes has exactly the rights the migrations gave
// maktaba_worker: on every table, sequence and function, the database, the schema and the
// default privileges. A migration that grants or revokes on maktaba_worker without adding
// its line to workerGroupGrants fails here. (A group of another name is made, never the
// shared maktaba_worker dropped: roles are the whole cluster's.)
func TestWorkerGroupAsTheMigrationsMadeIt(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	group := "maktaba_wg_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if err := ensureGroup(ctx, db, group); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP OWNED BY ` + group)
		_, _ = db.Exec(`DROP ROLE IF EXISTS ` + group)
	})
	// made again (it exists now): nothing changes, nothing fails
	if err := ensureGroup(ctx, db, group); err != nil {
		t.Fatalf("a group that exists: %v", err)
	}
	want, got := groupRights(t, db, WorkerGroup), groupRights(t, db, group)
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: maktaba_worker %v, the group made again %v", k, w, got[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: only the group made again has it", k)
		}
	}
}

// groupRights are role's rights on the scratch database's public objects, by a key naming
// the object and the right.
func groupRights(t *testing.T, db *sql.DB, role string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	q := func(sqlText string, args ...any) {
		rows, err := db.Query(sqlText, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var v bool
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			out[k] = v
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
		q(fmt.Sprintf(`SELECT 'table '||c.relname||' %[1]s', has_table_privilege($1, c.oid, '%[1]s')
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm')`, p), role)
	}
	for _, p := range []string{"USAGE", "SELECT", "UPDATE"} {
		q(fmt.Sprintf(`SELECT 'sequence '||c.relname||' %[1]s', has_sequence_privilege($1, c.oid, '%[1]s')
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind = 'S'`, p), role)
	}
	q(`SELECT 'function '||p.oid::regprocedure::text, has_function_privilege($1, p.oid, 'EXECUTE')
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public'`, role)
	q(`SELECT 'database connect', has_database_privilege($1, current_database(), 'CONNECT')`, role)
	q(`SELECT 'schema usage', has_schema_privilege($1, 'public', 'USAGE')`, role)
	q(`SELECT 'default '||d.defaclobjtype::text||' '||a.privilege_type, true
  FROM pg_default_acl d, aclexplode(d.defaclacl) a
 WHERE a.grantee = (SELECT oid FROM pg_roles WHERE rolname = $1)`, role)
	return out
}
