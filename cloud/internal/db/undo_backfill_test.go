package db

import "testing"

func TestBackfillColumns(t *testing.T) {
	const add = "ALTER TABLE x ADD COLUMN y INT;"
	const drop = "ALTER TABLE x DROP COLUMN y;"
	for _, tc := range []struct {
		name string
		up   string
		down string
		safe bool
	}{
		{"add then backfill", add + "UPDATE x SET y = 7;", drop, true},
		{"existing column", add + "UPDATE x SET existing = 7;", drop, false},
		{"mixed columns", add + "UPDATE x SET y = 7, existing = 8;", drop, false},
		{"other table", add + "UPDATE other SET y = 7;", drop, false},
		{"other schema", add + "UPDATE other.x SET y = 7;", drop, false},
		{"added too late", "UPDATE x SET y = 7;" + add, drop, false},
		{"possibly preexisting", "ALTER TABLE x ADD COLUMN IF NOT EXISTS y INT; UPDATE x SET y = 7;", drop, false},
		{"delete CTE", add + "WITH d AS (DELETE FROM other RETURNING id) UPDATE x SET y = d.id FROM d;", drop, false},
		{"insert from CTE", add + "WITH d AS (UPDATE x SET y = 7 RETURNING y) INSERT INTO other SELECT y FROM d;", drop, false},
		{"returning", add + "UPDATE x SET y = 7 RETURNING y;", drop, false},
		{"side effect", add + "UPDATE x SET y = nextval('s');", drop, false},
		{"side effect from unquoted function", add + "UPDATE x SET y = s.id FROM emit_rows() s;", drop, false},
		{"side effect in default", "ALTER TABLE x ADD COLUMN y INT DEFAULT nextval('s'); UPDATE x SET y = DEFAULT;", drop, false},
		{"trigger DDL", add + "ALTER TABLE x DISABLE TRIGGER t; UPDATE x SET y = 7;", drop, false},
		{"missing drop", add + "UPDATE x SET y = 7;", "SELECT 1;", false},
		{"destructive Down", add + "UPDATE x SET y = 7;", drop + "DELETE FROM other;", false},
		{"drops old column", add + "UPDATE x SET y = 7;", drop + "ALTER TABLE x DROP COLUMN old;", false},
		{"cascading Down", add + "UPDATE x SET y = 7;", "ALTER TABLE x DROP COLUMN y CASCADE;", false},
		{"multi-column and joined UPDATE", `ALTER TABLE public.x ADD COLUMN y INT, ADD COLUMN z TEXT;
			UPDATE x AS t
			SET y = coalesce(u.id, 0), z = 'from,where;--'
			FROM other AS u WHERE t.id = u.id`,
			"ALTER TABLE public.x DROP COLUMN IF EXISTS y, DROP COLUMN z RESTRICT;", true},
		{"tuple assignment", add + "ALTER TABLE x ADD COLUMN z INT; UPDATE x SET (y, z) = (1, 2);", drop + "ALTER TABLE x DROP COLUMN z;", true},
		{"mixed tuple assignment", add + "UPDATE x SET (y, existing) = (1, 2);", drop, false},
		{"quoted identifiers", `ALTER TABLE "public" . "x;y" ADD COLUMN "a"" b" TEXT;
			UPDATE "public"."x;y" "as" SET "a"" b" = $text$'; -- delete$text$;`,
			`ALTER TABLE "x;y" DROP COLUMN "a"" b";`, true},
		{"case folding", `ALTER TABLE X ADD COLUMN Y INT; UPDATE "x" SET "y" = 7;`, drop, true},
		{"quoted case differs", `ALTER TABLE X ADD COLUMN "Y" INT; UPDATE x SET y = 7;`, drop, false},
		{"comments", add + "/* UPDATE x SET old = 8; /* inner */ */ UPDATE x SET y = 7;", drop, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backfillColumns(undoSQL(tc.up), undoSQL(tc.down)); (got != nil) != tc.safe {
				t.Fatalf("backfillColumns returned %v, want recognized=%v", got, tc.safe)
			}
		})
	}
}

func TestBackfillRecordsEveryReadSource(t *testing.T) {
	up := undoSQL(`ALTER TABLE x ADD COLUMN y INT;
		UPDATE x SET y = a.value FROM public.a JOIN "other"."b" ON a.id = "b".id, c
		WHERE c.id = x.id;`)
	down := undoSQL("ALTER TABLE x DROP COLUMN y;")
	got := backfillColumns(up, down)
	if len(got) != 4 {
		t.Fatalf("want one target and three read sources, got %v", got)
	}
	for _, name := range []string{`"public"."a"`, `"other"."b"`, `"public"."c"`} {
		if relation := got[name]; relation == nil || len(relation.written) != 0 {
			t.Errorf("read source %s missing or marked as updated", name)
		}
	}
}
