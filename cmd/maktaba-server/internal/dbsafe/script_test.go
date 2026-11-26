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
		"a COPY's names longer than the scan": {
			head + "-- Data for Name: wide; Type: TABLE DATA; Schema: public; Owner: -\n--\n\nCOPY public.wide (" +
				strings.Repeat("c0, ", 300000) + "c1) FROM stdin;\n1\tx\n\\.\n\n\n" + end, 1, false},
		"a COPY that is not FROM stdin": {
			head + "-- Data for Name: a; Type: TABLE DATA; Schema: public; Owner: -\n--\n\nCOPY public.a (id, title) TO stdout;\n" +
				block("a", "1\tx\n") + end, 1, true},
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
