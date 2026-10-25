//go:build integration

package pgbundle

import (
	"context"
	"database/sql"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// The app role administers the machines' roles the superuser made (roleAdminSQL), on a
// real Postgres: after the move from the developer stack (a restore that makes the
// source's roles as the superuser) it gives a machine a new password and lets a new one
// into the group; a home made before (its marker at an older revision, the grants gone)
// gets them at its next start, and a running one from MendClusterGrants (the supervisor's
// mend-roles). Without them Postgres 16 refuses the app role all of it.
func TestRealAppRoleAdministersTheMachinesRoles(t *testing.T) {
	home := linkPGHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	root, err := os.MkdirTemp("", "mkro") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: port}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	var ver int
	var dump string
	if err := b.withServer(ctx, io.Discard, func() error {
		rows, err := q.Query(ctx, "postgres", `SELECT current_setting('server_version_num')`)
		if err != nil || len(rows) != 1 {
			return err
		}
		ver, _ = strconv.Atoi(strings.TrimSpace(rows[0]))
		if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`; CREATE TABLE videos (id serial PRIMARY KEY);`); err != nil {
			return err
		}
		dump, err = b.Backup(ctx, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if ver < 160000 {
		t.Skip("Postgres 16's rule (an older server lets the app role alter any role)")
	}
	const node, fresh = "maktaba_node_0123456789abcdef0123456789abcdef", "maktaba_node_fedcba9876543210fedcba9876543210"
	// as the app role: the machine's new password, a new machine into the group
	administers := func(t *testing.T) {
		t.Helper()
		appURL, err := b.URL()
		if err != nil {
			t.Fatal(err)
		}
		app, err := sql.Open("postgres", appURL)
		if err != nil {
			t.Fatal(err)
		}
		defer app.Close()
		for _, stmt := range []string{
			`ALTER ROLE ` + node + ` LOGIN PASSWORD 'a-new-one'`,
			`DROP ROLE IF EXISTS ` + fresh,
			`CREATE ROLE ` + fresh + ` LOGIN PASSWORD 'another' IN ROLE ` + WorkerGroup,
		} {
			if _, err := app.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("the app role: %s: %v", stmt, err)
			}
		}
	}
	unmade := `REVOKE ` + WorkerGroup + ` FROM ` + AppRole + ` CASCADE; REVOKE ` + node + ` FROM ` + AppRole + ` CASCADE;`

	// the move from the developer stack: the source's roles, made by the superuser
	opts := RestoreOptions{Roles: []string{WorkerGroup, node}, Members: [][2]string{{node, WorkerGroup}}}
	if err := b.Restore(ctx, dump, opts, io.Discard, func(Querier) error {
		administers(t)
		// a home of an older release: the grants gone, its marker at the revision before
		return q.Exec(ctx, "postgres", unmade)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Data, clusterMarker), []byte("rev 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil { // its next start
		t.Fatal(err)
	}
	if !b.clusterDone() {
		t.Fatal("the marker is not at this revision")
	}
	if err := b.withServer(ctx, io.Discard, func() error {
		administers(t)
		// a running home: the supervisor's mend-roles
		if err := q.Exec(ctx, "postgres", unmade); err != nil {
			return err
		}
		if err := b.MendClusterGrants(ctx); err != nil {
			return err
		}
		administers(t)
		return b.MendClusterGrants(ctx) // again: nothing left to do, nothing fails
	}); err != nil {
		t.Fatal(err)
	}
}
