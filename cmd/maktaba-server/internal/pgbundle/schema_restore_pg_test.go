package pgbundle_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
)

// The home's own schema (every migration), put back as an update's rollback puts it back
// and checked whole: its tables, indexes, keys, triggers, functions, grants (the workers'
// and the replicas' groups, column grants, revoked functions) and default privileges, and
// every table's rows; the database's grant to the workers' group comes back.
func TestRestoreOfTheHomeSchemaIsWhole(t *testing.T) {
	programs := pgtest.Programs(t)
	ctx := context.Background()
	dir := t.TempDir()
	b, _ := pgtest.Bundle(ctx, t, programs, dir, false)
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	if err := pgtest.Migrator(t, dir)(ctx, appURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Exec(ctx, pgbundle.AppDatabase, `INSERT INTO app_settings (key, value) VALUES ('before.update', '1')
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	dump, err := b.DumpBeforeUpdate(ctx, "v0.3.41", "v0.3.42", 3)
	if err != nil {
		t.Fatal(err)
	}
	if pgbundle.DumpCounts(dump) == nil {
		t.Fatal("the dump has no counts")
	}
	if err := q.Exec(ctx, pgbundle.AppDatabase, `DELETE FROM app_settings WHERE key = 'before.update'`); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := b.RestoreRunning(ctx, dump, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "is put back whole") {
		t.Fatalf("the restore did not say it is whole:\n%s", log.String())
	}
	rows, err := q.Query(ctx, pgbundle.AppDatabase, `SELECT count(*) FROM app_settings WHERE key = 'before.update'`)
	if err != nil || len(rows) != 1 || rows[0] != "1" {
		t.Fatalf("the dump's row = %v, %v", rows, err)
	}
	rows, err = q.Query(ctx, "postgres", `SELECT datacl::text FROM pg_database WHERE datname = '`+pgbundle.AppDatabase+`'`)
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0], pgbundle.WorkerGroup+"=c/") {
		t.Fatalf("the database's grants after the restore = %v, %v", rows, err)
	}
}
