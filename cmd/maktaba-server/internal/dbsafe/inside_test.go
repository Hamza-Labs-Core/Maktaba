package dbsafe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/lib/pq"
)

// What a grant of a dump is on, as the item that defines it names it: a function by its
// signature — PG 16/18 type-only definition tags, arg-named ACL tags, and oidvectortypes
// normalized to one canonical key, preserving quoted type names verbatim — and a column's
// grant by its table, with real PG 16/18 dotted syntax (videos."a.b", "my.tbl"."a.b",
// "a""b.c"."d.e") as well as space-separated forms.
func TestGrantedOnBySignatureAndColumn(t *testing.T) {
	for _, c := range [][4]string{
		{"FUNCTION", "f(a integer, b public.vector)", "FUNCTION", "f(integer,public.vector)"},
		{"FUNCTION", "f(integer, public.vector)", "FUNCTION", "f(integer,public.vector)"},
		{"FUNCTION", "f()", "FUNCTION", "f()"},
		{"AGGREGATE", "agg0(*)", "FUNCTION", "agg0(*)"},
		{"AGGREGATE", "agg(int4)", "FUNCTION", "agg(int4)"},
		{"PROCEDURE", `"Odd ""One"(a text, b double precision)`, "FUNCTION", `Odd "One(text,double precision)`},
		{"FUNCTION", "f(a double precision)", "FUNCTION", "f(double precision)"},
		{"FUNCTION", "f(double   precision)", "FUNCTION", "f(double precision)"},
		{"FUNCTION", "f(double text)", "FUNCTION", "f(text)"},
		{"FUNCTION", "f(a character varying(20))", "FUNCTION", "f(character varying(20))"},
		{"FUNCTION", "f(ts timestamp with time zone)", "FUNCTION", "f(timestamp with time zone)"},
		{"FUNCTION", "f(VARIADIC a integer[])", "FUNCTION", "f(integer[])"},
		{"FUNCTION", "f(VARIADIC integer[])", "FUNCTION", "f(integer[])"},
		{"FUNCTION", "f(a integer DEFAULT 1 + 1)", "FUNCTION", "f(integer)"},
		{"FUNCTION", "f(a text DEFAULT 'a, b')", "FUNCTION", "f(text)"},
		{"FUNCTION", `f(a "My Type")`, "FUNCTION", `f("My Type")`},
		{"FUNCTION", `f("My Type")`, "FUNCTION", `f("My Type")`},
		{"FUNCTION", `f(a "MyType")`, "FUNCTION", `f("MyType")`},
		{"FUNCTION", `f("my arg" "My Type")`, "FUNCTION", `f("My Type")`},
		{"FUNCTION", `f(a public."My Type"[])`, "FUNCTION", `f(public."My Type"[])`},
		{"FUNCTION", `f(public."My Type"[])`, "FUNCTION", `f(public."My Type"[])`},
		{"TABLE", `"Odd Name"`, "TABLE", "Odd Name"},
		// Real PostgreSQL 16/18 pg_dump dotted COLUMN ACL tags
		{"COLUMN", `videos."a.b"`, "TABLE", "videos"},
		{"COLUMN", `"my.tbl"."a.b"`, "TABLE", "my.tbl"},
		{"COLUMN", `"a""b.c"."d.e"`, "TABLE", `a"b.c`},
		{"COLUMN", `"a""b.c"`, "TABLE", `a"b.c`},
		{"COLUMN", `users.password_hash`, "TABLE", "users"},
		// Space-separated forms
		{"COLUMN", `videos "a.b"`, "TABLE", "videos"},
		{"COLUMN", `"my table" col`, "TABLE", "my table"},
		{"COLUMN", `videos password_hash`, "TABLE", "videos"},
		{"COLUMN", "videos", "TABLE", "videos"},
	} {
		if kind, of := grantedOn(c[0], c[1]); kind != c[2] || of != c[3] {
			t.Errorf("%s %s: %q %q, want %q %q", c[0], c[1], kind, of, c[2], c[3])
		}
	}
	// Quoted type names with vs without spaces must never alias, and overloaded functions stay distinct.
	_, withSpace := grantedOn("FUNCTION", `f(a "My Type")`)
	_, withoutSpace := grantedOn("FUNCTION", `f(a "MyType")`)
	if withSpace == withoutSpace {
		t.Fatalf("quoted type names aliased: %q == %q", withSpace, withoutSpace)
	}
	_, ov1 := grantedOn("FUNCTION", "f(integer)")
	_, ov2 := grantedOn("FUNCTION", "f(text)")
	if ov1 == ov2 {
		t.Fatalf("overloaded functions aliased: %q == %q", ov1, ov2)
	}
	// PG 16/18 type-only definition tag matches arg-named ACL tag.
	_, defTag := grantedOn("FUNCTION", `f("My Type", public.vector, integer[])`)
	_, aclTag := grantedOn("FUNCTION", `f(arg1 "My Type", arg2 public.vector, VARIADIC arg3 integer[])`)
	if defTag != aclTag {
		t.Fatalf("definition tag %q != ACL tag %q", defTag, aclTag)
	}
}

// What a grant of a dump is on, as the item that defines it names it.
func TestGrantedOn(t *testing.T) {
	for _, c := range [][4]string{
		{"FUNCTION", "f(a integer, b public.vector)", "FUNCTION", "f(integer,public.vector)"},
		{"AGGREGATE", "agg(*)", "FUNCTION", "agg(*)"},
		{"PROCEDURE", `"Odd ""One"()`, "FUNCTION", `Odd "One()`},
		{"TABLE", `"Odd Name"`, "TABLE", "Odd Name"},
		{"MATERIALIZED VIEW", "m", "TABLE", "m"},
		{"COLUMN", "users.password_hash", "TABLE", "users"},
		{"DOMAIN", "d", "TYPE", "d"},
		{"SCHEMA", "public", "", ""},
	} {
		if kind, of := grantedOn(c[0], c[1]); kind != c[2] || of != c[3] {
			t.Errorf("%s %s: %q %q", c[0], c[1], kind, of)
		}
	}
}

// What a dump holds that no one transaction can put back.
func TestCannotHold(t *testing.T) {
	list := "1; 1259 16390 TABLE public videos maktaba\n" +
		"2; 0 0 BLOBS - 1 maktaba\n" +
		"3; 0 0 EVENT TRIGGER - on_change postgres\n" +
		"4; 2611 16392 SERVER - myserver postgres\n"
	if why := cannotHold(list); why != "large objects" {
		t.Fatalf("held: %q", why)
	}
	for _, l := range []string{
		"3; 0 0 EVENT TRIGGER - on_change postgres\n",
		"4; 2611 16392 SERVER - myserver postgres\n",
		"5; 0 0 FOREIGN DATA WRAPPER - myfdw postgres\n",
		"6; 0 0 USER MAPPING - myserver postgres\n",
	} {
		if why := cannotHold(l); why == "" {
			t.Errorf("nothing held in %q", l)
		}
	}
	for _, l := range []string{
		"1; 1259 16390 TABLE public videos maktaba\n",
		"2; 1255 16384 FUNCTION public f(integer) maktaba\n",
	} {
		if why := cannotHold(l); why != "" {
			t.Errorf("held %q in %q", why, l)
		}
	}
}

// What makes a schema or an extension that is there, its grants and comments, is left out of the
// list; all else stays, what is commented out already too.
func TestWithoutItems(t *testing.T) {
	lines := []string{
		";",
		"; Selected TOC Entries:",
		"6; 2615 16386 SCHEMA - textsearch maktaba_admin",
		"8; 2615 16390 SCHEMA - textsearch_other maktaba_admin",
		"9; 2615 2200 SCHEMA - public maktaba",
		"4701; 0 0 ACL - SCHEMA textsearch maktaba_admin",
		"4702; 0 0 COMMENT - SCHEMA textsearch maktaba_admin",
		"2; 3079 16387 EXTENSION - pg_textsearch ",
		"3; 3079 16400 EXTENSION - pg_trgm ",
		"4703; 0 0 COMMENT - EXTENSION pg_textsearch ",
		"4704; 0 0 COMMENT - EXTENSION pg_trgm \r",
		"4705; 0 0 ACL - SCHEMA public maktaba",
		";4706; 0 0 ACL - SCHEMA textsearch maktaba_admin",
		"219; 1259 16500 TABLE public textsearch maktaba",
		"220; 1259 16501 TABLE textsearch own maktaba",
		"4800; 0 0 COMMENT public TABLE textsearch maktaba",
		"",
	}
	out := strings.Split(withoutItems(strings.Join(lines, "\n"), map[string]bool{"SCHEMA - textsearch": true, "ACL - SCHEMA textsearch": true,
		"COMMENT - SCHEMA textsearch": true, "EXTENSION - pg_textsearch": true, "COMMENT - EXTENSION pg_textsearch": true, "ACL - SCHEMA public": false}), "\n")
	left := map[int]bool{2: true, 5: true, 6: true, 7: true, 9: true}
	for i, l := range lines {
		want := l
		if left[i] {
			want = ";" + l
		}
		if out[i] != want {
			t.Errorf("%q\nwant %q", out[i], want)
		}
	}
	whole := strings.Join(lines, "\n")
	if withoutItems(whole, map[string]bool{"EXTENSION - plpgsql": true}) != whole {
		t.Error("changed though nothing of it is there")
	}
}

// The dump's grants on the server's own objects (pg_catalog's) are left out of the list.
func TestWithoutCatalogACLs(t *testing.T) {
	lines := []string{
		"2; 1255 16384 FUNCTION public f(integer) maktaba",
		"3; 0 0 ACL pg_catalog FUNCTION pg_reload_conf() maktaba",
		"4; 0 0 ACL public FUNCTION f(integer) maktaba",
		";5; 0 0 ACL pg_catalog FUNCTION pg_create_logical_replication_slot(name, boolean) maktaba",
	}
	out := strings.Split(withoutCatalogACLs(strings.Join(lines, "\n")), "\n")
	if out[0] != lines[0] || out[2] != lines[2] || out[3] != lines[3] {
		t.Errorf("what stays changed:\n%s", strings.Join(out, "\n"))
	}
	if out[1] != ";"+lines[1] {
		t.Errorf("the catalog grant stays:\n%s", strings.Join(out, "\n"))
	}
}

// Who may make an extension again.
func TestMayMake(t *testing.T) {
	for _, c := range []struct{ super, create, available, needsSuper, trusted, want bool }{
		{super: true, available: true, needsSuper: true, want: true},
		{super: true, needsSuper: true}, // its package is gone
		{create: true, available: true, needsSuper: true},
		{create: true, available: true, needsSuper: true, trusted: true, want: true},
		{available: true, needsSuper: true, trusted: true},
		{available: true, want: true}, // superuser = false
	} {
		if mayMake(c.super, c.create, c.available, c.needsSuper, c.trusted) != c.want {
			t.Errorf("%+v", c)
		}
	}
}

// Which errors say the database's user may not make the extension there.
func TestMayNotMakeCode(t *testing.T) {
	for code, want := range map[string]bool{"42501": true, "42939": true, "42710": false, "42P07": false, "": false} {
		if got := mayNotMakeCode(pq.ErrorCode(code)); got != want {
			t.Errorf("%s: %v", code, got)
		}
	}
}

// The script that empties public spells a zero-argument aggregate's drop the way DROP
// AGGREGATE takes it ((*) — the object's name gives ()), and what goes with what is in
// public leaves a session's temporary tables out, with escaped LIKE wildcards so a permanent
// schema such as pgxtemp_data is never omitted.
func TestEmptyPublicAndWithInPublicSpellings(t *testing.T) {
	script := emptyPublic()
	if !strings.Contains(script, "regexp_replace(o.identity, '\\(\\)$', '(*)')") {
		t.Error("a zero-argument aggregate is dropped as agg(*), not agg()")
	}
	if !strings.Contains(withInPublic, "relpersistence = 't'") ||
		!strings.Contains(withInPublic, `nspname LIKE 'pg\_temp\_%' ESCAPE '\'`) {
		t.Error("a temporary table is not left out of what goes with what is in public")
	}
	if strings.Contains(withInPublic, "LIKE 'pg_temp%'") {
		t.Error("unescaped LIKE 'pg_temp%' matches permanent schemas such as pgxtemp_data")
	}
}

// Non-public objects created by a dump are collected for upfront collision checks, while
// public and system schemas are ignored.
func TestElsewhereObjectsCollectsNonPublicDefinitions(t *testing.T) {
	list := strings.Join([]string{
		"1; 1259 100 TABLE public videos maktaba",
		"2; 1259 101 TABLE custom notes maktaba",
		"3; 1259 102 VIEW custom v_notes maktaba",
		"4; 1255 103 FUNCTION custom f(a integer) maktaba",
		"5; 1247 104 TYPE custom \"My Type\" maktaba",
		"6; 0 0 ACL custom TABLE notes maktaba",
		"7; 0 0 ACL pg_catalog FUNCTION pg_reload_conf() maktaba",
		"8; 2615 105 SCHEMA - custom maktaba",
	}, "\n")
	got := elsewhereObjects(list)
	for _, want := range []string{
		"custom TABLE notes",
		"custom TABLE v_notes",
		"custom FUNCTION f(integer)",
		"custom TYPE My Type",
	} {
		if got[want] == "" {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("unexpected extra objects: %v", got)
	}
}

// Routine signature matching strips OUT/TABLE parameters to match proargtypes (e.g.
// pgrowlocks), handles function names containing '(' (quoted or unquoted), and never
// loops on nested '=' inside parentheses.
func TestRoutineSignatureOUTParensAndNestedEquals(t *testing.T) {
	// Real pg_dump ACL tag for pgrowlocks includes OUT params; oidvectortypes has input only.
	aclTag := "pgrowlocks(relname text, OUT locked_row tid, OUT locker xid, OUT multi boolean, OUT xids xid[], OUT modes text[], OUT pids integer[])"
	if _, aclSig := grantedOn("FUNCTION", aclTag); aclSig != "pgrowlocks(text)" {
		t.Errorf("pgrowlocks ACL sig = %q, want pgrowlocks(text)", aclSig)
	}
	if _, defSig := grantedOn("FUNCTION", "pgrowlocks(text)"); defSig != "pgrowlocks(text)" {
		t.Errorf("pgrowlocks def sig = %q, want pgrowlocks(text)", defSig)
	}
	if _, outOnly := grantedOn("FUNCTION", "f(OUT a integer, OUT b text)"); outOnly != "f()" {
		t.Errorf("OUT-only sig = %q, want f()", outOnly)
	}
	if _, mixed := grantedOn("FUNCTION", "f(IN a integer, OUT b text, INOUT c boolean, TABLE d int)"); mixed != "f(integer,boolean)" {
		t.Errorf("mixed modes sig = %q, want f(integer,boolean)", mixed)
	}
	// Function names containing '(' (both quoted and unquoted).
	if _, q := grantedOn("FUNCTION", `"f(x)"(integer)`); q != "f(x)(integer)" {
		t.Errorf("quoted paren name = %q, want f(x)(integer)", q)
	}
	if _, u := grantedOn("FUNCTION", `f(x)(integer, numeric(10, 2))`); u != "f(x)(integer,numeric(10,2))" {
		t.Errorf("unquoted paren name = %q, want f(x)(integer,numeric(10,2))", u)
	}
	// Nested '=' inside parentheses must terminate cleanly without stalling.
	if typ, ok := argType("a text DEFAULT foo(x=1)"); !ok || typ != "text" {
		t.Errorf("nested '=' in DEFAULT = (%q, %v), want (text, true)", typ, ok)
	}
	if toks := scanArgTokens("numeric(a=1)"); len(toks) == 0 {
		t.Errorf("scanArgTokens(numeric(a=1)) returned empty")
	}
}

// stubSQLDriver is a minimal in-memory database/sql driver for testing howTo's planner
// logic (non-public object collisions, pg_catalog ACL filtering before withoutOthers, and
// missing psql refusal in afresh mode) without a live Postgres server.
type stubSQLDriver struct {
	mu      sync.Mutex
	handler func(query string) ([][]driver.Value, error)
}

var (
	stubDriverOnce sync.Once
	stubDriver     = &stubSQLDriver{}
)

func openStubDB(t *testing.T, handler func(query string) ([][]driver.Value, error)) *sql.DB {
	t.Helper()
	stubDriverOnce.Do(func() { sql.Register("dbsafe_stub", stubDriver) })
	stubDriver.mu.Lock()
	prev := stubDriver.handler
	stubDriver.handler = handler
	stubDriver.mu.Unlock()
	t.Cleanup(func() {
		stubDriver.mu.Lock()
		stubDriver.handler = prev
		stubDriver.mu.Unlock()
	})
	d, err := sql.Open("dbsafe_stub", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func (d *stubSQLDriver) Open(_ string) (driver.Conn, error) { return stubConn{d: d}, nil }

type stubConn struct{ d *stubSQLDriver }

func (c stubConn) Prepare(_ string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c stubConn) Close() error                          { return nil }
func (c stubConn) Begin() (driver.Tx, error)             { return stubTx{}, nil }
func (c stubConn) BeginTx(_ context.Context, _ driver.TxOptions) (driver.Tx, error) {
	return stubTx{}, nil
}
func (c stubConn) ExecContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (c stubConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.mu.Lock()
	h := c.d.handler
	c.d.mu.Unlock()
	rows, err := h(query)
	if err != nil {
		return nil, err
	}
	return &stubRows{rows: rows}, nil
}

type stubTx struct{}

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

type stubRows struct {
	rows [][]driver.Value
	idx  int
}

func (r *stubRows) Columns() []string {
	if len(r.rows) == 0 {
		return []string{"c0"}
	}
	cols := make([]string, len(r.rows[0]))
	for i := range cols {
		cols[i] = "c"
	}
	return cols
}
func (r *stubRows) Close() error { return nil }
func (r *stubRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

// howTo refuses: (1) missing psql even in afresh mode (so CheckRestore never approves a
// restore or triggers a release switch without psql), (2) existing objects colliding in a
// non-public schema even for a superuser; and (3) explicitly strips pg_catalog ACLs before
// withoutOthers runs for non-superusers, while skipping SCHEMA - public under publicAfresh.
func TestHowToPlannerChecksCollisionPSQLAndCatalogACLs(t *testing.T) {
	ctx := context.Background()
	tg := Target{}

	t.Run("missing psql refused in afresh planner", func(t *testing.T) {
		d := openStubDB(t, func(q string) ([][]driver.Value, error) {
			switch {
			case strings.Contains(q, "SELECT current_user, r.rolsuper"):
				return [][]driver.Value{{"maktaba_admin", true, true, true, true}}, nil
			default:
				return nil, nil
			}
		})
		list := "1; 1259 100 TABLE public videos maktaba\n"
		_, err := tg.howTo(ctx, d, t.TempDir(), list, false)
		if !CannotRestore(err) || !strings.Contains(err.Error(), "takes psql, and there is none beside pg_restore") {
			t.Fatalf("expected missing psql refusal in planner, got: %v", err)
		}
	})

	t.Run("existing non-public object collision refused up front even for superuser", func(t *testing.T) {
		d := openStubDB(t, func(q string) ([][]driver.Value, error) {
			switch {
			case strings.Contains(q, "SELECT current_user, r.rolsuper"):
				return [][]driver.Value{{"maktaba_admin", true, true, true, true}}, nil
			case strings.Contains(q, "SELECT n.nspname, o.kind, o.name"):
				return [][]driver.Value{{"custom", "TABLE", "notes"}}, nil
			default:
				return nil, nil
			}
		})
		list := "1; 1259 100 TABLE custom notes maktaba\n"
		_, err := tg.howTo(ctx, d, t.TempDir(), list, true)
		if !CannotRestore(err) || !strings.Contains(err.Error(), "table custom.notes") {
			t.Fatalf("expected non-public collision refusal, got: %v", err)
		}
	})

	t.Run("non-superuser strips pg_catalog ACLs via withoutCatalogACLs before withoutOthers and keeps public ACL", func(t *testing.T) {
		d := openStubDB(t, func(q string) ([][]driver.Value, error) {
			switch {
			case strings.Contains(q, "SELECT current_user, r.rolsuper"):
				return [][]driver.Value{{"maktaba", false, true, true, true}}, nil
			case strings.Contains(q, "SELECT rolname FROM pg_roles"):
				return [][]driver.Value{{"maktaba"}}, nil
			default:
				return nil, nil
			}
		})
		list := strings.Join([]string{
			"1; 2615 2200 SCHEMA - public maktaba",
			"2; 1259 100 TABLE public videos maktaba",
			"3; 0 0 ACL - SCHEMA public maktaba",
			// A pg_catalog ACL whose object kind (PARAMETER) grantedOn ignores: only
			// withoutCatalogACLs catches it, proving howTo passes mine (not list) to withoutOthers.
			"4; 0 0 ACL pg_catalog PARAMETER log_statement maktaba_admin",
			"5; 0 0 ACL pg_catalog FUNCTION pg_reload_conf() maktaba_admin",
		}, "\n")
		h, err := tg.howTo(ctx, d, t.TempDir(), list, true)
		if err != nil {
			t.Fatalf("howTo: %v", err)
		}
		if h.inside {
			t.Fatalf("expected afresh, got inside")
		}
		if !strings.Contains(h.list, ";1; 2615 2200 SCHEMA - public maktaba") {
			t.Errorf("SCHEMA - public should be skipped under publicAfresh:\n%s", h.list)
		}
		if strings.Contains(h.list, ";3; 0 0 ACL - SCHEMA public maktaba") {
			t.Errorf("ACL - SCHEMA public should be retained:\n%s", h.list)
		}
		if !strings.Contains(h.list, ";4; 0 0 ACL pg_catalog PARAMETER log_statement maktaba_admin") {
			t.Errorf("pg_catalog PARAMETER ACL was not filtered before withoutOthers:\n%s", h.list)
		}
		if !strings.Contains(h.list, ";5; 0 0 ACL pg_catalog FUNCTION pg_reload_conf() maktaba_admin") {
			t.Errorf("pg_catalog FUNCTION ACL was not filtered:\n%s", h.list)
		}
	})
}
