package cluster

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Why a home's re-made access never reached its machine (the Mac home after its move from
// the developer stack into its bundled Postgres): on Postgres 16 a role that may create
// roles alters, grants or drops only a role it administers, which it does for those it
// made itself. The move (pgbundle's restore) makes the source's roles — the machines' and
// maktaba_worker — as the superuser, so the home's user is refused the machine's new
// password, and a new machine's place in the group, while Supported says yes. Once the
// superuser gives it ADMIN on them (pgbundle's roleAdminSQL: the cluster grants made
// again), both go through.
func TestARoleARestoreMadeIsNotTheHomesUntilMended(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t) // its maktaba_worker made by the superuser, as a restore makes it
	ctx := context.Background()
	var ver int
	if err := db.QueryRow(`SELECT current_setting('server_version_num')::int`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver < 160000 {
		t.Skip("Postgres 16's rule (an older server lets such a user alter any role)")
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	app := "maktaba_app_" + suffix
	node, fresh := RoleName(uuid.NewString()), RoleName(uuid.NewString())
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE ROLE ` + app + ` LOGIN PASSWORD 'app-pass' CREATEROLE`)
	t.Cleanup(func() {
		for _, r := range []string{node, fresh, app} {
			_, _ = db.Exec(`DROP ROLE IF EXISTS ` + pq.QuoteIdentifier(r))
		}
	})
	// what the restore makes of the source's machine (recreateSQL): its role, in the group
	exec(`CREATE ROLE ` + node + ` NOLOGIN`)
	exec(`GRANT ` + WorkerGroup + ` TO ` + node)
	home, err := sql.Open("postgres", clustertest.WithUser(t, dsn, app, "app-pass"))
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	roles := &PGRoles{DB: home}
	if ok, why := roles.Supported(ctx); !ok {
		t.Fatalf("supported: %s", why)
	}
	pw, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	refused := func(what string, err error) {
		t.Helper()
		var pe *pq.Error
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatalf("%s: %v, want permission denied (42501)", what, err)
		}
	}
	refused("the machine's new password", roles.Create(ctx, node, pw))
	refused("a new machine in the group", roles.Create(ctx, fresh, pw))

	// the mend, as roleAdminSQL grants it: ADMIN without the roles' rights
	exec(`GRANT ` + WorkerGroup + ` TO ` + app + ` WITH ADMIN OPTION, INHERIT FALSE, SET FALSE`)
	exec(`GRANT ` + node + ` TO ` + app + ` WITH ADMIN OPTION, INHERIT FALSE, SET FALSE`)
	if err := roles.Create(ctx, node, pw); err != nil {
		t.Fatalf("the machine's new password, mended: %v", err)
	}
	if err := roles.Create(ctx, fresh, pw); err != nil {
		t.Fatalf("a new machine, mended: %v", err)
	}
	if ok, err := roles.Usable(ctx, node); err != nil || !ok {
		t.Fatalf("the machine's role logs in: %v %v", ok, err)
	}
}
