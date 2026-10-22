package dbfence

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
)

// A fenced database refuses the app's roles' writes, the ones made in a transaction that
// says READ WRITE (lib/pq's) too, and the tables' owner's DDL and grants (it may not give
// its rights back to itself, nor a migration change the schema); then it gives every right
// back exactly as it was: the owner's, a group's, one a migration took from that group, a
// table's grant option.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install.
func TestAFencedDatabaseRefusesTheAppsWrites(t *testing.T) {
	programs := pgtest.Programs(t)
	root, err := os.MkdirTemp("", "mkfence") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx := context.Background()
	b, _ := pgtest.Bundle(ctx, t, programs, root, false)
	superURL, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	super, app := open(t, superURL), open(t, appURL)
	exec := func(db *sql.DB, q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(super, `CREATE ROLE maktaba_worker NOLOGIN`)
	exec(app, `CREATE TABLE videos (id serial PRIMARY KEY, title text)`)
	exec(app, `CREATE TABLE users (id serial PRIMARY KEY, name text)`)
	exec(app, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO maktaba_worker`)
	exec(app, `REVOKE ALL ON users FROM maktaba_worker`)
	exec(app, `GRANT TRUNCATE ON videos TO maktaba_worker WITH GRANT OPTION`)
	acls := func() string {
		t.Helper()
		var s string
		if err := super.QueryRowContext(ctx, `SELECT string_agg(relname || '=' || coalesce(relacl::text, 'default'), ' ' ORDER BY relname)
		    FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relkind = 'r'`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := acls()

	// taken twice (a freeze asked again): recorded once
	for range 2 {
		if err := Take(ctx, super); err != nil {
			t.Fatal(err)
		}
	}
	if taken, err := Taken(ctx, super); err != nil || !taken {
		t.Fatalf("taken: %v %v", taken, err)
	}
	tx, err := app.BeginTx(ctx, &sql.TxOptions{}) // lib/pq: BEGIN READ WRITE
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO videos (title) VALUES ('x')`); err == nil ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the app role wrote a fenced table: %v", err)
	}
	_ = tx.Rollback()
	for _, q := range []string{`UPDATE users SET name = 'x'`, `DELETE FROM videos`, `TRUNCATE videos`} {
		if _, err := app.ExecContext(ctx, q); err == nil {
			t.Fatalf("%s went through", q)
		}
	}
	// the owner may not take its rights back, nor change the schema, in a transaction or not
	for _, q := range []string{`GRANT INSERT ON videos TO CURRENT_USER`, `GRANT ALL ON ALL TABLES IN SCHEMA public TO maktaba_worker`,
		`ALTER TABLE videos ADD COLUMN extra int`, `CREATE TABLE later (id int)`, `DROP TABLE users`,
		`ALTER DEFAULT PRIVILEGES GRANT INSERT ON TABLES TO maktaba_worker`} {
		if _, err := app.ExecContext(ctx, q); err == nil || !strings.Contains(err.Error(), "fenced") {
			t.Fatalf("%s while fenced: %v", q, err)
		}
	}
	tx, err = app.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `GRANT INSERT ON videos TO CURRENT_USER`); err == nil {
		t.Fatal("the owner gave itself its right back in a transaction")
	}
	_ = tx.Rollback()
	var n int
	if err := app.QueryRowContext(ctx, `SELECT count(*) FROM videos`).Scan(&n); err != nil {
		t.Fatalf("the app role cannot read: %v", err)
	}
	if _, err := app.ExecContext(ctx, `SELECT count(*) FROM maktaba_fence.writes`); err == nil {
		t.Fatal("the app role reads the fence's record")
	}
	// the superuser writes (a switch's own steps, a subscription's apply), and changes the
	// schema (a subscription's refresh)
	exec(super, `INSERT INTO videos (title) VALUES ('kept')`)
	exec(super, `CREATE TABLE super_made (id int)`)
	exec(super, `DROP TABLE super_made`)

	if err := Release(ctx, super); err != nil {
		t.Fatal(err)
	}
	if got := acls(); got != before {
		t.Fatalf("the rights given back differ:\n%s\nwas\n%s", got, before)
	}
	if taken, err := Taken(ctx, super); err != nil || taken {
		t.Fatalf("still taken: %v %v", taken, err)
	}
	exec(app, `INSERT INTO videos (title) VALUES ('again')`)
	exec(app, `CREATE TABLE later (id int)`)
	if err := Release(ctx, super); err != nil { // nothing taken: nothing to do
		t.Fatal(err)
	}
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
