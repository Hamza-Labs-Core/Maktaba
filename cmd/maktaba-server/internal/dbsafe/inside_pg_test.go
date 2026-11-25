package dbsafe

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// insideRig is a library of the real migrations whose database user is no superuser, with
// pgvector in public (and, on 18, pg_textsearch in a schema of its own) made by one.
type insideRig struct {
	*pgRig
	tg Target // as that user
}

func newInsideRig(t *testing.T) *insideRig {
	t.Helper()
	r := &insideRig{pgRig: newPGRig(t)}
	if !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql")
	}
	u, err := r.b.URL()
	if err != nil {
		t.Fatal(err)
	}
	r.tg = r.t
	r.tg.URL = u
	var vector, super bool
	if err := r.db.QueryRow(`SELECT EXISTS (SELECT FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'vector' AND n.nspname = 'public'),
		(SELECT rolsuper FROM pg_roles WHERE rolname = $1)`, pgbundle.AppRole).Scan(&vector, &super); err != nil || super {
		t.Fatalf("the rig's user is a superuser: %v %v", super, err)
	}
	if !vector { // a Postgres with no pgvector (CI's 16): another that needs a superuser
		if _, err := r.db.Exec(`CREATE EXTENSION pageinspect SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *insideRig) exec(t *testing.T, tg Target, q string) {
	t.Helper()
	d, err := tg.open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (r *insideRig) one(t *testing.T, q string) (s string) {
	t.Helper()
	d, err := r.t.open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n sql.NullString
	if err := d.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n.String
}

// schema is all of the database but its rows, owners and grants too, as pg_dump writes it.
func (r *insideRig) schema(t *testing.T) string {
	t.Helper()
	cmd, err := r.t.command(context.Background(), "pg_dump", "", "--schema-only")
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v %s", err, errs.String())
	}
	// pg_dump's word of its own each time, and the parentheses an expression read again loses
	text := regexp.MustCompile(`(?m)^\\(un)?restrict .*$|[()]`).ReplaceAllString(out.String(), "")
	// grants on the schema taken back and made again are in another order
	lines := strings.Split(text, "\n")
	var at []int
	var grants []string
	for i, l := range lines {
		if strings.HasPrefix(l, "GRANT USAGE ON SCHEMA public ") {
			at, grants = append(at, i), append(grants, l)
		}
	}
	slices.Sort(grants)
	for i, n := range at {
		lines[n] = grants[i]
	}
	return strings.Join(lines, "\n")
}

func (r *insideRig) extensions(t *testing.T) string {
	return r.one(t, `SELECT string_agg(extname || ' ' || oid || ' ' || extnamespace::regnamespace, ', ' ORDER BY extname) FROM pg_extension`)
}

// what an update, or a person, may leave in public: every kind of thing
const leftInPublic = `
	CREATE TABLE after_update (id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY, t text);
	CREATE TABLE after_parts (id int) PARTITION BY RANGE (id);
	CREATE TABLE after_part PARTITION OF after_parts FOR VALUES FROM (0) TO (10);
	CREATE TABLE after_serial (id serial, "Odd Name" text);
	CREATE VIEW after_view AS SELECT * FROM after_update;
	CREATE MATERIALIZED VIEW after_mat AS SELECT * FROM after_view;
	CREATE SEQUENCE after_seq;
	CREATE TYPE after_enum AS ENUM ('a');
	CREATE TYPE after_comp AS (a int, b after_enum);
	CREATE TYPE after_range AS RANGE (subtype = int);
	CREATE DOMAIN after_domain AS text CHECK (VALUE <> '');
	CREATE FUNCTION after_fn(a after_enum, b after_domain DEFAULT 'x') RETURNS int LANGUAGE sql AS 'SELECT 1';
	CREATE FUNCTION after_fn(a int) RETURNS int LANGUAGE sql AS 'SELECT 1';
	CREATE PROCEDURE after_proc(a int) LANGUAGE sql AS 'SELECT 1';
	CREATE AGGREGATE after_agg(int) (SFUNC = int4pl, STYPE = int);
	CREATE FUNCTION after_op(a after_comp, b after_comp) RETURNS boolean LANGUAGE sql AS 'SELECT true';
	CREATE OPERATOR === (LEFTARG = after_comp, RIGHTARG = after_comp, FUNCTION = after_op);
	CREATE TEXT SEARCH CONFIGURATION after_ts (COPY = simple);
	CREATE TEXT SEARCH DICTIONARY after_dict (TEMPLATE = simple);
	CREATE COLLATION after_coll (LOCALE = 'C');
	CREATE STATISTICS after_stat ON id, t FROM after_update;
	CREATE CONVERSION after_conv FOR 'LATIN1' TO 'UTF8' FROM iso8859_1_to_utf8;
	ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO PUBLIC;
	ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON TYPES TO maktaba_worker;
	GRANT SELECT ON users TO maktaba_worker;
	REVOKE USAGE ON SCHEMA public FROM maktaba_worker;
	ALTER TABLE videos ADD COLUMN after_col after_enum;
	DROP TABLE app_settings CASCADE;`

// A database user who may not make pgvector again has a backup put back inside public: every
// object, grant and default privilege is as the backup's, every row too, and the extensions are
// the ones that were there. Again and again; and by one whose public is another's.
func TestRestoreInsidePublic(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	r.exec(t, r.tg, `INSERT INTO app_settings (key, value) VALUES ('inside.probe', '1')`)
	if r.one(t, `SELECT count(*) FROM pg_extension WHERE extname = 'vector'`) == "1" {
		r.exec(t, r.tg, `CREATE TABLE vec_t (id int, e vector(3)); INSERT INTO vec_t VALUES (1, '[1,2,3]'), (2, '[4,5,6]');
			CREATE INDEX vec_t_idx ON vec_t USING hnsw (e vector_l2_ops)`)
	} else {
		r.exec(t, r.tg, `CREATE TABLE vec_t (id int); INSERT INTO vec_t VALUES (1), (2)`)
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if inside, err := r.tg.CheckRestore(ctx, dump); err != nil || !inside {
		t.Fatalf("inside %v, %v", inside, err)
	}
	if inside, err := r.t.CheckRestore(ctx, dump); err != nil || inside {
		t.Fatalf("a superuser's: inside %v, %v", inside, err)
	}
	want, exts := r.schema(t), r.extensions(t)
	for _, when := range []string{"once", "again", "public another's"} {
		if when == "public another's" {
			r.exec(t, r.t, `ALTER SCHEMA public OWNER TO `+pgbundle.SuperUser+`; GRANT USAGE, CREATE ON SCHEMA public TO `+pgbundle.AppRole)
			want = r.schema(t)
		}
		before, _, err := r.tg.Identity(ctx)
		if err != nil || before == "" {
			t.Fatal(before, err)
		}
		r.exec(t, r.tg, leftInPublic)
		// a session of the user's own that holds a lock is ended, though a superuser's, which it
		// may not end, is there too
		super, err := r.t.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := super.Exec(`SELECT 1`); err != nil {
			t.Fatal(err)
		}
		own, err := r.tg.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := own.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`LOCK TABLE users`); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		var steps []string
		if err := r.tg.RestoreWith(ctx, dump, func(step string) error { steps = append(steps, step); return nil }, &log); err != nil {
			t.Fatalf("%s: %v\n%s", when, err, log.String())
		}
		if !slices.Equal(steps, []string{pgbundle.StepInside}) {
			t.Fatalf("%s: told %v", when, steps)
		}
		_ = tx.Rollback()
		_, _ = own.Close(), super.Close()
		if got := r.schema(t); got != want {
			t.Fatalf("%s: another schema:\n%s", when, diffLines(want, got))
		}
		if got := r.extensions(t); got != exts {
			t.Fatalf("%s: the extensions are %s\nwere %s", when, got, exts)
		}
		if n := r.one(t, `SELECT (SELECT count(*) FROM app_settings WHERE key = 'inside.probe') || ' ' || (SELECT count(*) FROM vec_t)`); n != "1 2" {
			t.Fatalf("%s: rows %s", when, n)
		}
		if id, _, err := r.tg.Identity(ctx); err != nil || id == before {
			t.Fatalf("%s: it is told from the one it replaced by nothing: %s %v", when, id, err)
		}
	}
}

// diffLines are the first lines that differ.
func diffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	in := func(ls []string) map[string]int {
		m := map[string]int{}
		for _, l := range ls {
			m[l]++
		}
		return m
	}
	var out []string
	wm, gm := in(w), in(g)
	for _, l := range w {
		if l != "" && l != "--" && wm[l] > gm[l] {
			out = append(out, "- "+l)
		}
	}
	for _, l := range g {
		if l != "" && l != "--" && gm[l] > wm[l] {
			out = append(out, "+ "+l)
		}
	}
	if len(out) == 0 {
		return "the same lines in another order"
	}
	return strings.Join(out[:min(len(out), 40)], "\n")
}

// One that fails part way, or whose rows are short, commits nothing: public is as it was.
func TestRestoreInsidePublicThatFailsCommitsNothing(t *testing.T) {
	r := newInsideRig(t)
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell")
	}
	ctx := context.Background()
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	r.exec(t, r.tg, leftInPublic)
	want, exts := r.schema(t), r.extensions(t)
	before, _, err := r.tg.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pg_restore"), []byte("#!/bin/sh\n'"+r.t.bin("pg_restore")+"' \"$@\" || exit 1\n"+
		"case \"$*\" in *=post-data*) echo 'SELECT nothing FROM nowhere;';; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tg := r.tg
	tg.Bin = func(n string) string {
		if n == "pg_restore" {
			return filepath.Join(bin, n)
		}
		return r.t.bin(n)
	}
	if err := tg.Restore(ctx, dump, nil); err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Fatal(err)
	}
	id, _, err := r.tg.Identity(ctx)
	if err != nil || id != before || r.extensions(t) != exts {
		t.Fatalf("identity %s, was %s; %v", id, before, err)
	}
	if got := r.schema(t); got != want {
		t.Fatalf("another schema:\n%s", diffLines(want, got))
	}
}

// What cannot be put back inside public is refused before anything is touched, and says why.
func TestRestoreInsidePublicIsRefusedUpFront(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	r.exec(t, r.t, `CREATE EXTENSION pgstattuple SCHEMA public`)
	more := filepath.Join(r.t.Backups, "pre-1.5.0-to-1.6.0.dump")
	if err := r.tg.Dump(ctx, more); err != nil {
		t.Fatal(err)
	}
	r.exec(t, r.t, `DROP EXTENSION pgstattuple`)
	r.exec(t, r.tg, `CREATE TYPE mine_t AS ENUM ('a')`)
	noPSQL := r.tg
	noPSQL.Bin = func(n string) string {
		if n == "psql" {
			return filepath.Join(t.TempDir(), "no-such-psql")
		}
		return r.t.bin(n)
	}
	session, err := r.tg.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	conn, err := session.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for name, c := range map[string]struct{ do, undo, dump, want string }{
		"another's table":                    {`CREATE TABLE public.theirs (id int)`, `DROP TABLE public.theirs`, dump, "table public.theirs (" + pgbundle.SuperUser + ")"},
		"another's function":                 {`CREATE FUNCTION public.theirs() RETURNS int LANGUAGE sql AS 'SELECT 1'`, `DROP FUNCTION public.theirs()`, dump, "function public.theirs()"},
		"an extension the database lacks":    {`SELECT 1`, `SELECT 1`, more, "the extension pgstattuple, which the database lacks"},
		"another's view elsewhere":           {`CREATE SCHEMA rep; CREATE VIEW rep.v AS SELECT id FROM public.users`, `DROP SCHEMA rep CASCADE`, dump, "view rep.v (" + pgbundle.SuperUser + ")"},
		"another's column of a type there":   {`CREATE TYPE public.mine AS ENUM ('a'); ALTER TYPE public.mine OWNER TO ` + pgbundle.AppRole + `; CREATE SCHEMA rep; CREATE TABLE rep.t (id int, c public.mine)`, `DROP SCHEMA rep CASCADE; DROP TYPE public.mine`, dump, "table column rep.t.c (" + pgbundle.SuperUser + ")"},
		"another's partition":                {`CREATE TABLE public.parts (id int) PARTITION BY RANGE (id); ALTER TABLE public.parts OWNER TO ` + pgbundle.AppRole + `; CREATE SCHEMA rep; CREATE TABLE rep.p PARTITION OF public.parts FOR VALUES FROM (0) TO (9); INSERT INTO rep.p VALUES (1)`, `DROP TABLE public.parts; DROP SCHEMA rep`, dump, "table rep.p (" + pgbundle.SuperUser + ")"},
		"another's key to a table there":     {`CREATE SCHEMA rep; CREATE TABLE rep.k (id uuid REFERENCES public.videos (id))`, `DROP SCHEMA rep CASCADE`, dump, "rep.k (" + pgbundle.SuperUser + ")"},
		"a member of an extension":           {`CREATE FUNCTION public.member(a public.mine_t) RETURNS int LANGUAGE sql AS 'SELECT 1'; ALTER EXTENSION pgcrypto ADD FUNCTION public.member(public.mine_t)`, `ALTER EXTENSION pgcrypto DROP FUNCTION public.member(public.mine_t); DROP FUNCTION public.member(public.mine_t)`, dump, "extension pgcrypto"},
		"a public it may make nothing in":    {`ALTER SCHEMA public OWNER TO ` + pgbundle.SuperUser + `; REVOKE CREATE ON SCHEMA public FROM PUBLIC, ` + pgbundle.AppRole, `ALTER SCHEMA public OWNER TO pg_database_owner`, dump, "GRANT CREATE ON SCHEMA public TO " + pgbundle.AppRole},
		"another's with the schema in order": {`CREATE TYPE public.theirs AS ENUM ('a')`, `DROP TYPE public.theirs`, dump, "type public.theirs"},
	} {
		r.exec(t, r.t, c.do)
		before := r.schema(t)
		err := r.tg.Restore(ctx, c.dump, nil)
		if err == nil || !CannotRestore(err) || !strings.Contains(err.Error(), "(nothing changed)") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if _, cerr := r.tg.CheckRestore(ctx, c.dump); !CannotRestore(cerr) {
			t.Errorf("%s: the check says %v", name, cerr)
		}
		if r.schema(t) != before {
			t.Errorf("%s: something changed", name)
		}
		r.exec(t, r.t, c.undo)
	}
	// for its chunks where it has some (pgvector there), for the one transaction where it has none
	why := "which takes psql, and there is none beside pg_restore"
	if len(pgbundle.Chunks(dump)) > 0 {
		why = "chunks that psql loads, and there is no psql beside pg_restore"
	}
	if err := noPSQL.Restore(ctx, dump, nil); !CannotRestore(err) || !strings.Contains(err.Error(), why) {
		t.Errorf("without psql: %v", err)
	}
	var one int
	if err := conn.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Errorf("a session was ended: %v", err)
	}
	// with it all undone it goes on
	if inside, err := r.tg.CheckRestore(ctx, dump); err != nil || !inside {
		t.Errorf("then: inside %v, %v", inside, err)
	}
}

// A rehearsal for a user who may not make the database's extensions in a scratch database is
// skipped, saying so, and leaves nothing: it does not fail the update.
func TestRehearsalSkipsWhatTheUserMayNotMake(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	mig := r.migrationsWith(t, map[string]string{"9001_probe.sql": "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"})
	r.exec(t, r.t, `ALTER ROLE `+pgbundle.AppRole+` CREATEDB`)
	res, err := r.tg.Rehearse(ctx, r.api, mig, 20, nil)
	if err != nil || !strings.Contains(res.Skipped, "may not make the extension") || res.Pending != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if left := r.scratchLeft(t); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(r.t.Backups, ".rehearse-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	if res, err := r.t.Rehearse(ctx, r.api, mig, 20, nil); err != nil || res.Skipped != "" {
		t.Fatalf("a superuser's: %+v %v", res, err)
	}
}

// A public schema that is another's, with no extension in it, is not dropped either: the backup
// is put back inside it, with its grants left as they are.
func TestRestoreInsideAnothersPublic(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	if _, err := r.db.Exec(`CREATE DATABASE plain_check OWNER ` + pgbundle.AppRole + ` TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if d, err := r.t.open(ctx, ""); err == nil {
			_, _ = d.Exec(`DROP DATABASE IF EXISTS plain_check WITH (FORCE)`)
			_ = d.Close()
		}
	})
	super, plain := r.t, r.tg
	var err error
	if super.URL, err = r.t.urlFor("plain_check"); err != nil {
		t.Fatal(err)
	}
	if plain.URL, err = r.tg.urlFor("plain_check"); err != nil {
		t.Fatal(err)
	}
	r.exec(t, super, `ALTER SCHEMA public OWNER TO `+pgbundle.SuperUser+`; GRANT USAGE, CREATE ON SCHEMA public TO `+pgbundle.AppRole)
	r.exec(t, plain, `CREATE TABLE own (id int); INSERT INTO own VALUES (1)`)
	dump := filepath.Join(r.t.Backups, "plain.dump")
	if err := plain.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	r.exec(t, plain, `INSERT INTO own VALUES (2); CREATE TABLE more (id int)`)
	if inside, err := plain.CheckRestore(ctx, dump); err != nil || !inside {
		t.Fatalf("inside %v, %v", inside, err)
	}
	if err := plain.Restore(ctx, dump, nil); err != nil {
		t.Fatal(err)
	}
	d, err := super.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var rows int
	var owner, more string
	if err := d.QueryRow(`SELECT (SELECT count(*) FROM own), (SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'public'),
		coalesce(to_regclass('public.more')::text, '')`).Scan(&rows, &owner, &more); err != nil || rows != 1 || owner != pgbundle.SuperUser || more != "" {
		t.Fatalf("%d rows, public is %s's, more %q, %v", rows, owner, more, err)
	}
}

// Grants on an extension's own objects, which its owner made, are the owner's: a user who could
// only be warned of them is given the whole backup without them, and it is found whole.
func TestRestoreLeavesOthersGrantsOut(t *testing.T) {
	ctx := context.Background()
	restored := func(t *testing.T, tg Target, dump string) {
		t.Helper()
		for range 2 {
			var log bytes.Buffer
			if err := tg.Restore(ctx, dump, &log); err != nil {
				t.Fatalf("%v\n%s", err, log.String())
			}
		}
	}
	const onAll = `GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO maktaba_worker`
	t.Run("demoted", func(t *testing.T) { // who made 0103's grants on them; then an administrator made the extension again
		r := newInsideRig(t)
		r.exec(t, r.t, `ALTER ROLE `+pgbundle.AppRole+` SUPERUSER`)
		r.exec(t, r.tg, onAll)
		r.exec(t, r.t, `ALTER ROLE `+pgbundle.AppRole+` NOSUPERUSER`)
		dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
		if err := r.tg.Dump(ctx, dump); err != nil {
			t.Fatal(err)
		}
		ext := r.one(t, `SELECT e.extname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
			WHERE n.nspname = 'public' AND e.extname IN ('vector', 'pageinspect')`)
		r.exec(t, r.t, `DROP EXTENSION `+ext+` CASCADE; CREATE EXTENSION `+ext+` SCHEMA public`)
		restored(t, r.tg, dump)
	})
	t.Run("new database", func(t *testing.T) {
		r := newInsideRig(t)
		of := func(db string) (super, plain Target) {
			t.Helper()
			r.exec(t, r.t, `CREATE DATABASE `+db+` OWNER `+pgbundle.AppRole+` TEMPLATE template0`)
			t.Cleanup(func() {
				if d, err := r.t.open(ctx, ""); err == nil {
					_, _ = d.Exec(`DROP DATABASE IF EXISTS ` + db + ` WITH (FORCE)`)
					_ = d.Close()
				}
			})
			super, plain = r.t, r.tg
			var err error
			if super.URL, err = r.t.urlFor(db); err != nil {
				t.Fatal(err)
			}
			if plain.URL, err = r.tg.urlFor(db); err != nil {
				t.Fatal(err)
			}
			r.exec(t, super, `CREATE EXTENSION pageinspect SCHEMA public`)
			return super, plain
		}
		super, plain := of("grants_from")
		r.exec(t, plain, `CREATE TABLE own (id int); INSERT INTO own VALUES (1), (2);
			CREATE FUNCTION mine(a int) RETURNS int LANGUAGE sql AS 'SELECT 1'; REVOKE EXECUTE ON FUNCTION mine(int) FROM PUBLIC`)
		r.exec(t, super, onAll)
		dump := filepath.Join(r.t.Backups, "from.dump")
		if err := plain.Dump(ctx, dump); err != nil {
			t.Fatal(err)
		}
		super, plain = of("grants_to")
		restored(t, plain, dump)
		d, err := super.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		var rows int
		var mine string
		if err := d.QueryRow(`SELECT (SELECT count(*) FROM own), (SELECT proacl::text FROM pg_proc WHERE proname = 'mine')`).Scan(&rows, &mine); err != nil ||
			rows != 2 || !strings.Contains(mine, "maktaba_worker=X") || strings.Contains(mine, "{=X") {
			t.Fatalf("%d rows, the user's own function's grants %s, %v", rows, mine, err)
		}
	})
	t.Run("taken back", func(t *testing.T) {
		r := newInsideRig(t)
		fn := r.one(t, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_depend x ON x.classid = 'pg_proc'::regclass AND x.objid = p.oid AND x.deptype = 'e'
			JOIN pg_extension e ON e.oid = x.refobjid WHERE e.extname IN ('vector', 'pageinspect') ORDER BY 1 LIMIT 1`)
		r.exec(t, r.t, `GRANT EXECUTE ON FUNCTION `+fn+` TO maktaba_worker`)
		dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
		if err := r.tg.Dump(ctx, dump); err != nil {
			t.Fatal(err)
		}
		r.exec(t, r.t, `REVOKE EXECUTE ON FUNCTION `+fn+` FROM maktaba_worker`)
		restored(t, r.tg, dump)
	})
	t.Run("trusted", func(t *testing.T) { // a trusted one, in a public that is the user's
		r := newInsideRig(t)
		r.exec(t, r.t, `CREATE DATABASE grants_trusted OWNER `+pgbundle.AppRole+` TEMPLATE template0`)
		t.Cleanup(func() {
			if d, err := r.t.open(ctx, ""); err == nil {
				_, _ = d.Exec(`DROP DATABASE IF EXISTS grants_trusted WITH (FORCE)`)
				_ = d.Close()
			}
		})
		super, plain := r.t, r.tg
		var err error
		if super.URL, err = r.t.urlFor("grants_trusted"); err != nil {
			t.Fatal(err)
		}
		if plain.URL, err = r.tg.urlFor("grants_trusted"); err != nil {
			t.Fatal(err)
		}
		r.exec(t, plain, `CREATE EXTENSION hstore; CREATE TABLE own (id int, h hstore); INSERT INTO own VALUES (1, 'a=>1')`)
		r.exec(t, super, onAll)
		dump := filepath.Join(r.t.Backups, "trusted.dump")
		if err := plain.Dump(ctx, dump); err != nil {
			t.Fatal(err)
		}
		if inside, err := plain.CheckRestore(ctx, dump); err != nil || inside {
			t.Fatalf("inside %v, %v", inside, err)
		}
		restored(t, plain, dump)
	})
}

// Default privileges of another user, an administrator's for this one or for all it makes, are
// neither in the way nor taken: a backup with them goes in without, and those there stay.
func TestRestoreLeavesOthersDefaultPrivileges(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	const common = `ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO ` + pgbundle.AppRole
	r.exec(t, r.t, common+`; ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO maktaba_worker`)
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	want := r.schema(t)
	if err := r.tg.Restore(ctx, dump, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.schema(t); got != want {
		t.Fatalf("another schema:\n%s", diffLines(want, got))
	}
	// in the backup only
	r.exec(t, r.t, strings.Replace(common, "GRANT ALL ON TABLES TO", "REVOKE ALL ON TABLES FROM", 1)+
		`; ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM maktaba_worker`)
	want = r.schema(t)
	if err := r.tg.Restore(ctx, dump, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.schema(t); got != want {
		t.Fatalf("then: another schema:\n%s", diffLines(want, got))
	}
}

// A backup that needs an extension the database lacks is refused up front, where public is the
// user's to make again too.
func TestRestoreRefusesAMissingExtension(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	if _, err := r.db.Exec(`CREATE DATABASE lacks_check OWNER ` + pgbundle.AppRole + ` TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if d, err := r.t.open(ctx, ""); err == nil {
			_, _ = d.Exec(`DROP DATABASE IF EXISTS lacks_check WITH (FORCE)`)
			_ = d.Close()
		}
	})
	super, plain := r.t, r.tg
	var err error
	if super.URL, err = r.t.urlFor("lacks_check"); err != nil {
		t.Fatal(err)
	}
	if plain.URL, err = r.tg.urlFor("lacks_check"); err != nil {
		t.Fatal(err)
	}
	r.exec(t, super, `CREATE EXTENSION pageinspect; CREATE EXTENSION hstore`)
	r.exec(t, plain, `CREATE TABLE own (id int); INSERT INTO own VALUES (1)`)
	dump := filepath.Join(r.t.Backups, "lacks.dump")
	if err := plain.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	r.exec(t, super, `DROP EXTENSION pageinspect; DROP EXTENSION hstore`)
	noPSQL := plain
	noPSQL.Bin = func(n string) string {
		if n == "psql" {
			return filepath.Join(t.TempDir(), "no-such-psql")
		}
		return r.t.bin(n)
	}
	for _, tg := range []Target{plain, noPSQL} {
		if err := tg.Restore(ctx, dump, nil); !CannotRestore(err) || !strings.Contains(err.Error(), "the extension pageinspect, which the database lacks") {
			t.Fatal(err)
		}
	}
	// made there by one who may, with a trusted one the user makes itself, it goes on
	r.exec(t, super, `CREATE EXTENSION pageinspect`)
	if err := plain.Restore(ctx, dump, nil); err != nil {
		t.Fatal(err)
	}
}

// The script that empties public stops, with nothing dropped, when an extension would go with it,
// or something is there when it ends: what the look before it did not see.
func TestEmptyPublicStops(t *testing.T) {
	r := newInsideRig(t)
	ctx := context.Background()
	run := func(want string) {
		t.Helper()
		d, err := r.t.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		tx, err := d.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(emptyPublic())
		_ = tx.Rollback()
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Fatalf("%v, want %q", err, want)
		}
	}
	run("")
	r.exec(t, r.t, `CREATE TYPE public.mine_t AS ENUM ('a'); CREATE FUNCTION public.member(a public.mine_t) RETURNS int LANGUAGE sql AS 'SELECT 1';
		ALTER EXTENSION pgcrypto ADD FUNCTION public.member(public.mine_t)`)
	run("an extension depends on")
	r.exec(t, r.t, `ALTER EXTENSION pgcrypto DROP FUNCTION public.member(public.mine_t)`)
	r.exec(t, r.t, `CREATE SCHEMA hook; CREATE FUNCTION hook.again() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN CREATE TABLE IF NOT EXISTS public.again (id int); END $$;
		CREATE EVENT TRIGGER again ON sql_drop EXECUTE FUNCTION hook.again()`)
	defer r.exec(t, r.t, `DROP EVENT TRIGGER again`)
	run("could not be emptied")
}
