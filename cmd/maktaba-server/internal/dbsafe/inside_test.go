package dbsafe

import (
	"strings"
	"testing"

	"github.com/lib/pq"
)

// What a grant of a dump is on, as the item that defines it names it: a function by its
// signature — the two spellings the list carries made one — and a column's grant by its
// table, the column's own name taken whole, dots included.
func TestGrantedOnBySignatureAndColumn(t *testing.T) {
	for _, c := range [][4]string{
		{"FUNCTION", "f(a integer, b public.vector)", "FUNCTION", "f(integer,public.vector)"},
		{"FUNCTION", "f(integer,public.vector)", "FUNCTION", "f(integer,public.vector)"},
		{"FUNCTION", "f()", "FUNCTION", "f()"},
		{"AGGREGATE", "agg0(*)", "FUNCTION", "agg0(*)"},
		{"AGGREGATE", "agg(int4)", "FUNCTION", "agg(int4)"},
		{"PROCEDURE", `"Odd ""One"(a text, b double precision)`, "FUNCTION", `Odd "One(text,doubleprecision)`},
		{"FUNCTION", "f(a double precision)", "FUNCTION", "f(doubleprecision)"},
		{"FUNCTION", "f(double precision)", "FUNCTION", "f(doubleprecision)"},
		{"FUNCTION", "f(VARIADIC a integer[])", "FUNCTION", "f(VARIADICinteger[])"},
		{"FUNCTION", "f(a integer DEFAULT 1 + 1)", "FUNCTION", "f(integer)"},
		{"FUNCTION", `f(a "My Type")`, "FUNCTION", `f("MyType")`},
		{"TABLE", `"Odd Name"`, "TABLE", "Odd Name"},
		{"COLUMN", `videos "a.b"`, "TABLE", "videos"},
		{"COLUMN", `"my table" col`, "TABLE", "my table"},
		{"COLUMN", `videos password_hash`, "TABLE", "videos"},
		{"COLUMN", "videos", "TABLE", "videos"}, // not two names: as before
	} {
		if kind, of := grantedOn(c[0], c[1]); kind != c[2] || of != c[3] {
			t.Errorf("%s %s: %q %q, want %q %q", c[0], c[1], kind, of, c[2], c[3])
		}
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
// public leaves a session's temporary tables out, by their persistence and their schema.
func TestEmptyPublicAndWithInPublicSpellings(t *testing.T) {
	script := emptyPublic()
	if !strings.Contains(script, "regexp_replace(o.identity, '\\(\\)$', '(*)')") {
		t.Error("a zero-argument aggregate is dropped as agg(*), not agg()")
	}
	if !strings.Contains(withInPublic, "relpersistence = 't'") || !strings.Contains(withInPublic, "nspname LIKE 'pg_temp%'") {
		t.Error("a temporary table is not left out of what goes with what is in public")
	}
}
