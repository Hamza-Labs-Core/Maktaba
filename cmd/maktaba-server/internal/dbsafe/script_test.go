package dbsafe

import (
	"strings"
	"testing"
)

// What of a script is rows and what is not, however it is cut into writes.
func TestCopyScan(t *testing.T) {
	block := func(table, rows string) string {
		return "--\n-- Data for Name: " + table + "; Type: TABLE DATA; Schema: public; Owner: -\n--\n\nCOPY public." + table +
			" (id, title) FROM stdin;\n" + rows + "\\.\n\n\n"
	}
	cut := func(table, rows string) string { // its COPY's end is not there
		return strings.TrimSuffix(block(table, rows), "\\.\n\n\n")
	}
	head := "SET statement_timeout = 0;\nSELECT pg_catalog.set_config('search_path', '', false);\n"
	end := "SELECT pg_catalog.setval('public.s', 1, true);\n--\n-- PostgreSQL database dump complete\n--\n\n"
	for name, c := range map[string]struct {
		script string
		tables int
		ok     bool
	}{
		"whole":                           {head + block("a", "1\tx\n") + block("b", "") + end, 2, true},
		"windows' ends of line":           {strings.ReplaceAll(head+block("a", "1\tx\n")+end, "\n", "\r\n"), 1, true},
		"a row that looks like a COPY":    {head + block("a", "COPY public.b (id) FROM stdin;\n") + end, 1, true},
		"a row that looks like an end":    {head + block("a", "1\t\\\\.\n") + end, 1, true},
		"a very long row":                 {head + block("a", "1\t"+strings.Repeat("x", 3<<20)+"\n") + block("b", "2\ty\n") + end, 2, true},
		"a function that holds a COPY":    {head + "CREATE FUNCTION public.f() RETURNS text LANGUAGE sql AS $$\nSELECT '\nCOPY public.b (id) FROM stdin;\n\\.\n'\n$$;\n" + block("a", "1\n") + "GRANT SELECT ON TABLE public.a TO r;\n" + end, 1, true},
		"no rows at all":                  {head + end, 0, true},
		"the last table cut":              {head + block("a", "1\tx\n") + cut("b", "2"), 2, false},
		"the last table cut, then SQL":    {head + cut("b", "2\n") + end, 1, false},
		"a table cut, the next one whole": {head + cut("a", "1\n") + block("b", "2\ty\n") + end, 2, false},
		"a table's rows missing":          {head + block("a", "1\tx\n") + end, 2, false},
	} {
		for _, size := range []int{1, 7, 4096, 1 << 30} {
			if size == 1 && len(c.script) > 1<<20 {
				continue
			}
			var s copyScan
			for rest := c.script; rest != ""; {
				n := min(size, len(rest))
				if w, err := s.Write([]byte(rest[:n])); w != n || err != nil {
					t.Fatal(w, err)
				}
				rest = rest[n:]
			}
			if err := s.whole(c.tables); (err == nil) != c.ok {
				t.Errorf("%s, %d bytes at a time: %v", name, size, err)
			}
		}
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
