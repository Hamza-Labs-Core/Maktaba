//go:build integration

package db_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// undoWorld is a database with migrations 1 and 2 (the release before), 10 (a deploy's, numbered
// above) and 5 (a deploy's, numbered below what was applied: an epic's late slot).
func undoWorld(t *testing.T, deploy map[string]string) (*db.Pool, *db.Migrator) {
	t.Helper()
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	// a schema of its own beside the cloud's: goose's table is the cloud's, so start it empty
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id > 0`); err != nil {
		t.Fatal(err)
	}
	base := fstest.MapFS{
		"00000001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE ua (id INT PRIMARY KEY, kept TEXT);\n-- +goose Down\nDROP TABLE ua;\n")},
		"00000002_b.sql": {Data: []byte("-- +goose Up\nINSERT INTO ua VALUES (1, 'x');\n-- +goose Down\nDELETE FROM ua;\n")},
	}
	if err := db.NewMigrator(pool, base, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	all := fstest.MapFS{}
	for k, v := range base {
		all[k] = v
	}
	for k, v := range deploy {
		all[k] = &fstest.MapFile{Data: []byte(v)}
	}
	m := db.NewMigrator(pool, all, ".")
	if err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool, m
}

func has(t *testing.T, pool *db.Pool, q string) bool {
	t.Helper()
	var ok bool
	if err := pool.DB.QueryRow(`SELECT ` + q).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

const (
	late = "-- +goose Up\nALTER TABLE ua ADD COLUMN late INT NOT NULL DEFAULT 7, ADD COLUMN note TEXT;\n-- +goose Down\nALTER TABLE ua DROP COLUMN IF EXISTS late, DROP COLUMN note;\n"
	tabl = "-- +goose Up\nCREATE TABLE ub (id INT);\nCREATE INDEX ub_i ON ub (id);\n-- +goose Down\nDROP INDEX ub_i;\nDROP TABLE IF EXISTS ub CASCADE;\n"
)

// Exactly the deploy's versions are undone, the newest applied first, a late lower slot included;
// what the release before had stays.
func TestUndoTakesBackADeploysMigrations(t *testing.T) {
	ctx := context.Background()
	pool, m := undoWorld(t, map[string]string{"00000005_late.sql": late, "00000010_t.sql": tabl})
	if got, err := m.Applied(ctx); err != nil || !slices.Equal(got, []int64{1, 2, 5, 10}) {
		t.Fatalf("applied %v %v", got, err)
	}
	if err := m.Undo(ctx, []int64{5, 10}); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Applied(ctx); err != nil || !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("applied after: %v %v", got, err)
	}
	if has(t, pool, `to_regclass('ub') IS NOT NULL`) || has(t, pool, `EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ua' AND column_name IN ('late', 'note'))`) {
		t.Fatal("the deploy's schema is still there")
	}
	if !has(t, pool, `EXISTS (SELECT 1 FROM ua WHERE kept = 'x')`) {
		t.Fatal("the release before's rows went")
	}
	// and up again: a Down followed by an Up is a whole round
	if err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
}

// Backfill values in migration-owned columns are disposable. A sibling
// column that was added but not backfilled still gets the ordinary value check.
func TestUndoAdditiveBackfill(t *testing.T) {
	ctx := context.Background()
	const script = `-- +goose Up
ALTER TABLE public.ua ADD COLUMN "filled column" TEXT, ADD COLUMN spare TEXT;
UPDATE "public"."ua" AS a SET "filled column" = '(' || a.kept || ')' WHERE a.id = 1;
-- +goose Down
ALTER TABLE ua DROP COLUMN "filled column", DROP COLUMN spare;
`
	pool, m := undoWorld(t, map[string]string{"00000010_fill.sql": script})
	if _, err := pool.DB.Exec(`UPDATE ua SET spare = 'written'`); err != nil {
		t.Fatal(err)
	}
	if err := m.Undo(ctx, []int64{10}); !errors.Is(err, db.ErrUndoKeeps) {
		t.Fatalf("unfilled addition has a new value: %v", err)
	}
	if _, err := pool.DB.Exec(`UPDATE ua SET spare = NULL`); err != nil {
		t.Fatal(err)
	}
	if err := m.Undo(ctx, []int64{10}); err != nil {
		t.Fatal(err)
	}
	if has(t, pool, `EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ua' AND column_name IN ('filled column', 'spare'))`) ||
		!has(t, pool, `EXISTS (SELECT 1 FROM ua WHERE kept = 'x')`) {
		t.Fatal("wrong schema or data after undo")
	}
}

func TestUndoBackfillWithUpdateTrigger(t *testing.T) {
	ctx := context.Background()
	const trigger = `-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION ua_before_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.kept := 'changed by trigger';
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER ua_before_update BEFORE UPDATE ON ua FOR EACH ROW EXECUTE FUNCTION ua_before_update();
-- +goose Down
DROP TRIGGER ua_before_update ON ua;
DROP FUNCTION ua_before_update();
`
	const backfill = `-- +goose Up
ALTER TABLE ua ADD COLUMN y INT;
UPDATE ua SET y = 7;
-- +goose Down
ALTER TABLE ua DROP COLUMN y;
`
	pool, m := undoWorld(t, map[string]string{"00000005_trigger.sql": trigger, "00000010_fill.sql": backfill})
	if !has(t, pool, `EXISTS (SELECT 1 FROM ua WHERE kept = 'changed by trigger')`) {
		t.Fatal("the backfill did not fire the trigger")
	}
	if err := m.Undo(ctx, []int64{10}); !errors.Is(err, db.ErrUndoLossy) {
		t.Fatalf("trigger-backed update must be held: %v", err)
	}
}

// A Down script that would delete rows written since the deploy is not run, nor any other of it.
func TestUndoKeepsWhatWasWrittenSince(t *testing.T) {
	ctx := context.Background()
	for name, write := range map[string]string{
		"a row in a new table":            `INSERT INTO ub VALUES (1)`,
		"a value in a new column":         `UPDATE ua SET note = 'written'`,
		"another value than the default":  `UPDATE ua SET late = 8`,
		"the default set to null instead": `ALTER TABLE ua ALTER late DROP NOT NULL; UPDATE ua SET late = NULL`,
	} {
		t.Run(name, func(t *testing.T) {
			pool, m := undoWorld(t, map[string]string{"00000005_late.sql": late, "00000010_t.sql": tabl})
			if _, err := pool.DB.Exec(write); err != nil {
				t.Fatal(err)
			}
			if err := m.Undo(ctx, []int64{5, 10}); !errors.Is(err, db.ErrUndoKeeps) {
				t.Fatalf("%v, want ErrUndoKeeps", err)
			}
			if got, _ := m.Applied(ctx); !slices.Equal(got, []int64{1, 2, 5, 10}) || !has(t, pool, `to_regclass('ub') IS NOT NULL`) {
				t.Fatalf("something was undone: %v", got)
			}
		})
	}
	// a type dropped takes the columns of its type with it
	_, m0 := undoWorld(t, map[string]string{"00000010_t.sql": "-- +goose Up\nCREATE TYPE ut AS ENUM ('a');\nALTER TABLE ua ADD COLUMN t ut;\n-- +goose Down\nDROP TYPE ut CASCADE;\n"})
	if err := m0.Undo(ctx, []int64{10}); !errors.Is(err, db.ErrUndoKeeps) {
		t.Fatalf("%v, want ErrUndoKeeps", err)
	}
	// its own statement changes rows
	_, m := undoWorld(t, map[string]string{"00000010_d.sql": "-- +goose Up\nCREATE TABLE uc (id INT);\n-- +goose Down\nDELETE FROM ua;\nDROP TABLE uc;\n"})
	if err := m.Undo(ctx, []int64{10}); !errors.Is(err, db.ErrUndoKeeps) {
		t.Fatalf("%v, want ErrUndoKeeps", err)
	}
}

// An Up that lost data cannot be undone, nor one with no Down; nothing runs, not even the others.
func TestUndoRefusesWhatCannotBeUndone(t *testing.T) {
	ctx := context.Background()
	for name, sql := range map[string]string{
		"a CTE deletes":  "-- +goose Up\nWITH gone AS (DELETE FROM ua RETURNING id) SELECT count(*) FROM gone;\n-- +goose Down\nSELECT 1;\n",
		"a DO block":     "-- +goose Up\n-- +goose StatementBegin\nDO $$ BEGIN DELETE FROM ua; END $$;\n-- +goose StatementEnd\n-- +goose Down\nSELECT 1;\n",
		"an alias":       "-- +goose Up\nUPDATE ua AS u SET kept = 'y';\n-- +goose Down\nSELECT 1;\n",
		"a contract":     "-- contract: nothing reads it\n-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n",
		"rows deleted":   "-- +goose Up\nDELETE FROM ua;\n-- +goose Down\nSELECT 1;\n",
		"rows changed":   "-- +goose Up\nUPDATE ua SET kept = 'y';\n-- +goose Down\nSELECT 1;\n",
		"a column gone":  "-- +goose Up\nALTER TABLE ua DROP COLUMN kept;\n-- +goose Down\nALTER TABLE ua ADD COLUMN kept TEXT;\n",
		"a bare drop":    "-- +goose Up\nALTER TABLE ua DROP kept;\n-- +goose Down\nALTER TABLE ua ADD COLUMN kept TEXT;\n",
		"a table gone":   "-- +goose Up\nDROP TABLE ua;\n-- +goose Down\nCREATE TABLE ua (id INT);\n",
		"a type dropped": "-- +goose Up\nCREATE TYPE ut AS ENUM ('a');\nDROP TYPE ut CASCADE;\n-- +goose Down\nSELECT 1;\n",
		"a type recast":  "-- +goose Up\nALTER TABLE ua ALTER COLUMN kept TYPE VARCHAR(9);\n-- +goose Down\nALTER TABLE ua ALTER COLUMN kept TYPE TEXT;\n",
		"no Down script": "-- +goose Up\nCREATE TABLE ud (id INT);\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, m := undoWorld(t, map[string]string{"00000010_x.sql": sql, "00000011_t.sql": tabl})
			if err := m.Undo(ctx, []int64{10, 11}); !errors.Is(err, db.ErrUndoLossy) {
				t.Fatalf("%v, want ErrUndoLossy", err)
			}
			if got, _ := m.Applied(ctx); !slices.Equal(got, []int64{1, 2, 10, 11}) {
				t.Fatalf("something was undone: %v", got)
			}
		})
	}
	// said so by the migration itself, and a constraint or a default dropped loses no rows
	for name, sql := range map[string]string{
		"down-restores":            "-- +maktaba down-restores: the Down sets it back\n-- +goose Up\nUPDATE ua SET kept = 'y';\n-- +goose Down\nALTER TABLE ua ALTER kept DROP DEFAULT;\n",
		"a constraint, dropped":    "-- +goose Up\nALTER TABLE ua DROP CONSTRAINT ua_pkey;\n-- +goose Down\nALTER TABLE ua ADD PRIMARY KEY (id);\n",
		"an upsert, a foreign key": "-- +goose Up\nCREATE TABLE ug (id INT PRIMARY KEY REFERENCES ua (id) ON UPDATE SET NULL);\nINSERT INTO ua VALUES (1) ON CONFLICT (id) DO UPDATE SET kept = 'x';\n-- +goose Down\nDROP TABLE ug;\n",
		"a json column":            "-- +goose Up\nALTER TABLE ua ADD COLUMN j JSON NOT NULL DEFAULT '{}';\n-- +goose Down\nALTER TABLE ua DROP COLUMN j;\n",
		"a function's body":        "-- +goose Up\n-- +goose StatementBegin\nCREATE FUNCTION uf() RETURNS void AS $$ BEGIN DELETE FROM ua; END $$ LANGUAGE plpgsql;\n-- +goose StatementEnd\n-- +goose Down\nDROP FUNCTION uf();\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, m := undoWorld(t, map[string]string{"00000010_x.sql": sql})
			if err := m.Undo(ctx, []int64{10}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A Down that fails is an error of its own (neither refusal), and a version not applied is named.
func TestUndoFailures(t *testing.T) {
	ctx := context.Background()
	_, m := undoWorld(t, map[string]string{"00000010_x.sql": "-- +goose Up\nCREATE TABLE ue (id INT);\n-- +goose Down\nSELECT no_such_function();\n"})
	err := m.Undo(ctx, []int64{10})
	if err == nil || errors.Is(err, db.ErrUndoKeeps) || errors.Is(err, db.ErrUndoLossy) {
		t.Fatalf("a failing Down: %v", err)
	}
	if err := m.Undo(ctx, []int64{99}); err == nil || !strings.Contains(err.Error(), "99 is not applied") {
		t.Fatalf("%v", err)
	}
	// a version another release's file holds (the running image has no file for it)
	if err := db.NewMigrator(dbtest.Migrated(t), fstest.MapFS{}, ".").Undo(ctx, []int64{500001}); !errors.Is(err, db.ErrUndoLossy) {
		t.Fatalf("no file: %v", err)
	}
}

// Every migration there is can be judged: none fails the reading, and an expansion (one that says
// neither contract nor down-restores, and whose Up changes no rows) is not taken for a loss.
func TestUndoJudgesEveryMigration(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	m := db.NewMigrator(pool, migrations.FS, ".")
	applied, err := m.Applied(ctx)
	if err != nil || len(applied) < 40 {
		t.Fatalf("%d applied, %v", len(applied), err)
	}
	ents, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	judged := 0
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		text, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		verdict, err := m.Judge(ctx, string(text))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
		if e.Name() == "00580004_security_retention.sql" && verdict.Err != nil {
			t.Errorf("%s: %s", e.Name(), verdict)
		}
		judged++
		t.Logf("%s: %s", e.Name(), verdict)
	}
	if judged != len(applied) {
		t.Errorf("%d judged, %d applied", judged, len(applied))
	}
}

// One that can't be undone is never hidden by another that is only better kept, whichever of them
// was applied last, nor by one whose rows could not be read: the previous release must not be
// started on a schema it can't run on.
func TestUndoTheGravestRefusalWins(t *testing.T) {
	ctx := context.Background()
	contract := "-- contract: nothing reads it\n-- +goose Up\nALTER TABLE ua DROP COLUMN kept;\n-- +goose Down\nALTER TABLE ua ADD COLUMN kept TEXT;\n"
	seeded := "-- +goose Up\nCREATE TABLE uh (id INT);\nINSERT INTO uh VALUES (1);\n-- +goose Down\nDROP TABLE uh;\n"
	unreadable := "-- +goose Up\nCREATE TABLE ui (id INT);\n-- +goose Down\nDROP TABLE a.b.c.d;\n"
	for name, deploy := range map[string]map[string]string{
		"the contract first":           {"00000010_c.sql": contract, "00000011_s.sql": seeded},
		"the contract last":            {"00000010_s.sql": seeded, "00000011_c.sql": contract},
		"between two kept":             {"00000010_s.sql": seeded, "00000011_c.sql": contract, "00000012_t.sql": tabl},
		"behind one that is not read":  {"00000010_c.sql": contract, "00000011_u.sql": unreadable},
		"and the Down script is empty": {"00000010_n.sql": "-- +goose Up\nCREATE TABLE uj (id INT);\n", "00000011_s.sql": seeded},
	} {
		t.Run(name, func(t *testing.T) {
			_, m := undoWorld(t, deploy)
			before, _ := m.Applied(ctx)
			err := m.Undo(ctx, before[2:])
			if !errors.Is(err, db.ErrUndoLossy) || errors.Is(err, db.ErrUndoKeeps) {
				t.Fatalf("%v, want ErrUndoLossy", err)
			}
			if after, _ := m.Applied(ctx); !slices.Equal(before, after) {
				t.Fatalf("something was undone: %v", after)
			}
		})
	}
	// and each refusal names every migration behind it
	_, m := undoWorld(t, map[string]string{"00000010_s.sql": seeded, "00000011_s.sql": strings.ReplaceAll(seeded, "uh", "uk")})
	if err := m.Undo(ctx, []int64{10, 11}); !errors.Is(err, db.ErrUndoKeeps) || !strings.Contains(err.Error(), "00000010_s.sql") || !strings.Contains(err.Error(), "00000011_s.sql") {
		t.Fatalf("%v", err)
	}
}
