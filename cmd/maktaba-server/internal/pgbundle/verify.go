package pgbundle

// A restore is checked before it counts (the outage of 2026-10-28: a parallel restore
// that failed part way left every table's rows but no vectors, none of the 630 indexes,
// keys, constraints and triggers and none of the 286 grants, and nothing said what was
// missing). VerifyRestore holds the restored database against the dump's table of
// contents: every table, view, sequence, index, constraint, trigger and function of the
// public schema is there, every grant and default privilege the dump has is, and every
// table holds the rows the dump took (counts.json, written in the dump's snapshot; a dump
// from before it: the chunked tables at least hold rows when their chunks do).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CountsFile is the file of a directory-format dump with every table's rows as the dump
// took them (its snapshot): {"public.videos": 1234, ...}. pg_restore reads only what its
// toc.dat names.
const CountsFile = "counts.json"

// TOCEntry is one item of a dump's table of contents (a line of pg_restore -l).
type TOCEntry struct {
	Desc   string // TABLE, INDEX, FK CONSTRAINT, ACL, ...
	Schema string // "-" for none
	Name   string // the object's name (a constraint's and a trigger's: "<table> <name>")
	Owner  string
	Line   string
}

// tocDescs are the kinds of a TOC line, the longest first (a kind is a prefix of another).
var tocDescs = []string{"MATERIALIZED VIEW DATA", "MATERIALIZED VIEW", "TEXT SEARCH CONFIGURATION",
	"TEXT SEARCH DICTIONARY", "SEQUENCE OWNED BY", "PUBLICATION TABLE", "CHECK CONSTRAINT", "FK CONSTRAINT",
	"DEFAULT ACL", "SEQUENCE SET", "TABLE DATA", "INDEX ATTACH", "ROW SECURITY", "CONSTRAINT", "PUBLICATION",
	"EXTENSION", "AGGREGATE", "PROCEDURE", "SEQUENCE", "FUNCTION", "TRIGGER", "COMMENT", "DEFAULT", "DOMAIN",
	"POLICY", "SCHEMA", "INDEX", "TABLE", "VIEW", "TYPE", "RULE", "ACL"}

// ParseTOC reads pg_restore -l's output: the items it lists, but those commented out (a
// line that starts with ';' is not restored).
func ParseTOC(list string) []TOCEntry {
	var out []TOCEntry
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		_, rest, ok := strings.Cut(line, "; ")
		if !ok {
			continue
		}
		f := strings.SplitN(rest, " ", 3) // the catalog's and the object's oids, then the rest
		if len(f) < 3 {
			continue
		}
		rest = f[2]
		e := TOCEntry{Line: line}
		for _, d := range tocDescs {
			if strings.HasPrefix(rest, d+" ") {
				e.Desc, rest = d, rest[len(d)+1:]
				break
			}
		}
		if e.Desc == "" {
			continue
		}
		words := strings.Fields(rest)
		if len(words) < 2 {
			continue
		}
		e.Schema = words[0]
		if len(words) >= 3 {
			e.Owner = words[len(words)-1]
			e.Name = strings.Join(words[1:len(words)-1], " ")
		} else {
			e.Name = words[1]
		}
		out = append(out, e)
	}
	return out
}

// isACL reports whether a TOC line is a grant (ACL) or a default privilege (DEFAULT ACL).
func isACL(line string) bool {
	for _, e := range ParseTOC(line) {
		return e.Desc == "ACL" || e.Desc == "DEFAULT ACL"
	}
	return false
}

// QueryFunc runs one statement that selects one text column, and returns its rows.
type QueryFunc func(ctx context.Context, sql string) ([]string, error)

// Missing is what a restore lacks, by kind (VerifyRestore's error).
type Missing struct {
	Items map[string][]string // kind (index, constraint, grant, ...) -> names
	Rows  []string            // "<table>: <restored> of <dumped> rows"
}

// Error says what is missing, the first few names of each kind.
func (m *Missing) Error() string {
	var parts []string
	kinds := make([]string, 0, len(m.Items))
	for k := range m.Items {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		names := m.Items[k]
		shown := names
		if len(shown) > 8 {
			shown = append(append([]string{}, shown[:8]...), fmt.Sprintf("and %d more", len(names)-8))
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", len(names), k, strings.Join(shown, ", ")))
	}
	if len(m.Rows) > 0 {
		shown := m.Rows
		if len(shown) > 8 {
			shown = append(append([]string{}, shown[:8]...), fmt.Sprintf("and %d more", len(m.Rows)-8))
		}
		parts = append(parts, fmt.Sprintf("%d tables without all their rows (%s)", len(m.Rows), strings.Join(shown, "; ")))
	}
	return "the restored database is not whole: missing " + strings.Join(parts, "; ")
}

func (m *Missing) add(kind, name string) {
	if m.Items == nil {
		m.Items = map[string][]string{}
	}
	m.Items[kind] = append(m.Items[kind], name)
}

func (m *Missing) empty() bool { return len(m.Items) == 0 && len(m.Rows) == 0 }

// DumpCounts are the rows every table held when the dump was taken (its CountsFile; nil
// when it has none).
func DumpCounts(dump string) map[string]int64 {
	b, err := os.ReadFile(filepath.Join(dump, CountsFile))
	if err != nil {
		return nil
	}
	var c map[string]int64
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return c
}

// chunkHeader is the size of an empty binary COPY: its signature, flags, header extension
// and trailer.
const chunkHeader = 11 + 4 + 4 + 2

// VerifyRestore checks the restored database (query runs on it) against the dump's table
// of contents (list, pg_restore -l's output, as it was restored: the lines commented out
// were not) and its rows (DumpCounts, or the chunks); a *Missing error says what lacks.
func VerifyRestore(ctx context.Context, dump, list string, query QueryFunc) error {
	set := func(sql string) (map[string]bool, error) {
		rows, err := query(ctx, sql)
		if err != nil {
			return nil, err
		}
		out := make(map[string]bool, len(rows))
		for _, r := range rows {
			out[r] = true
		}
		return out, nil
	}
	const public = ` JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'`
	rels, err := set(`SELECT c.relkind::text || '|' || c.relname FROM pg_class c` + public)
	if err != nil {
		return err
	}
	// a grant the dump has: an ACL other than the object's default (a revoke counts too)
	relACL, err := set(`SELECT c.relname FROM pg_class c` + public + ` AND c.relacl IS NOT NULL
		AND c.relacl <> acldefault((CASE WHEN c.relkind = 'S' THEN 's' ELSE 'r' END)::"char", c.relowner)`)
	if err != nil {
		return err
	}
	cons, err := set(`SELECT c.relname || ' ' || o.conname FROM pg_constraint o JOIN pg_class c ON c.oid = o.conrelid` + public)
	if err != nil {
		return err
	}
	trigs, err := set(`SELECT c.relname || ' ' || t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid` + public +
		` AND NOT t.tgisinternal`)
	if err != nil {
		return err
	}
	colACL, err := set(`SELECT c.relname || '.' || a.attname FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid` + public +
		` AND cardinality(a.attacl) > 0`)
	if err != nil {
		return err
	}
	defACL, err := set(`SELECT pg_get_userbyid(d.defaclrole) || '|' || d.defaclobjtype::text FROM pg_default_acl d
		LEFT JOIN pg_namespace n ON n.oid = d.defaclnamespace WHERE coalesce(n.nspname, '-') IN ('public', '-')`)
	if err != nil {
		return err
	}
	fnRows, err := query(ctx, `SELECT p.proname || '|' || count(*) || '|' ||
		count(*) FILTER (WHERE p.proacl IS NOT NULL AND p.proacl <> acldefault('f'::"char", p.proowner)) FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' GROUP BY p.proname`)
	if err != nil {
		return err
	}
	fns, fnACLs := map[string]int{}, map[string]int{}
	for _, r := range fnRows {
		f := strings.Split(r, "|")
		if len(f) == 3 {
			fns[f[0]], _ = strconv.Atoi(f[1])
			fnACLs[f[0]], _ = strconv.Atoi(f[2])
		}
	}
	schemaACL, err := set(`SELECT nspname FROM pg_namespace WHERE nspname = 'public' AND nspacl IS NOT NULL`)
	if err != nil {
		return err
	}

	m := &Missing{}
	hasRel := func(kinds, name string) bool {
		for _, k := range kinds {
			if rels[string(k)+"|"+name] {
				return true
			}
		}
		return false
	}
	wantFns, wantFnACLs := map[string]int{}, map[string]int{}
	var dataTables []string
	for _, e := range ParseTOC(list) {
		if e.Schema != "public" && !(e.Desc == "ACL" && e.Schema == "-") && e.Desc != "DEFAULT ACL" {
			continue
		}
		switch e.Desc {
		case "TABLE":
			if !hasRel("rpf", e.Name) {
				m.add("tables", e.Name)
			}
		case "VIEW":
			if !hasRel("v", e.Name) {
				m.add("views", e.Name)
			}
		case "MATERIALIZED VIEW":
			if !hasRel("m", e.Name) {
				m.add("materialized views", e.Name)
			}
		case "SEQUENCE":
			if !hasRel("S", e.Name) {
				m.add("sequences", e.Name)
			}
		case "INDEX":
			if !hasRel("iI", e.Name) {
				m.add("indexes", e.Name)
			}
		case "CONSTRAINT", "FK CONSTRAINT", "CHECK CONSTRAINT":
			table, _, _ := strings.Cut(e.Name, " ")
			if hasRel("rpf", table) && !cons[e.Name] {
				m.add("constraints", e.Name)
			}
		case "TRIGGER":
			if !trigs[e.Name] {
				m.add("triggers", e.Name)
			}
		case "FUNCTION", "PROCEDURE", "AGGREGATE":
			name, _, _ := strings.Cut(e.Name, "(")
			wantFns[name]++
		case "TABLE DATA":
			dataTables = append(dataTables, e.Name)
		case "ACL":
			kind, name, _ := strings.Cut(e.Name, " ")
			switch kind {
			case "TABLE", "SEQUENCE", "VIEW", "MATERIALIZED":
				if kind == "MATERIALIZED" {
					name = strings.TrimPrefix(name, "VIEW ")
				}
				if !relACL[name] {
					m.add("grants", e.Name)
				}
			case "COLUMN":
				if !colACL[name] {
					m.add("grants", e.Name)
				}
			case "FUNCTION", "PROCEDURE":
				fn, _, _ := strings.Cut(name, "(")
				wantFnACLs[fn]++
			case "SCHEMA":
				if name == "public" && !schemaACL["public"] {
					m.add("grants", e.Name)
				}
			}
		case "DEFAULT ACL":
			objtype := map[string]string{"TABLES": "r", "SEQUENCES": "S", "FUNCTIONS": "f", "TYPES": "T", "SCHEMAS": "n"}
			words := strings.Fields(e.Name)
			if len(words) > 0 && objtype[words[len(words)-1]] != "" && !defACL[e.Owner+"|"+objtype[words[len(words)-1]]] {
				m.add("default privileges", e.Name+" "+e.Owner)
			}
		}
	}
	for name, n := range wantFns {
		if fns[name] < n {
			m.add("functions", name)
		}
	}
	for name, n := range wantFnACLs {
		if fnACLs[name] < n {
			m.add("grants", "FUNCTION "+name)
		}
	}
	if err := verifyRows(ctx, dump, dataTables, query, m); err != nil {
		return err
	}
	if m.empty() {
		return nil
	}
	for _, names := range m.Items {
		sort.Strings(names)
	}
	return m
}

// verifyRows checks every table's rows against the dump's counts; a dump without them: the
// chunked tables hold rows when their chunks do (the outage's empty segment_embeddings).
func verifyRows(ctx context.Context, dump string, dataTables []string, query QueryFunc, m *Missing) error {
	counts := DumpCounts(dump)
	want := map[string]int64{}
	if counts != nil {
		for _, t := range dataTables {
			if n, ok := counts["public."+t]; ok {
				want["public."+t] = n
			}
		}
	}
	nonEmpty := map[string]bool{}
	for table, files := range Chunks(dump) {
		if n, ok := counts[table]; ok {
			want[table] = n
			continue
		}
		var size int64
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				size += st.Size() - chunkHeader
			}
		}
		if size > 0 {
			nonEmpty[table] = true
		}
	}
	tables := make([]string, 0, len(want)+len(nonEmpty))
	for t := range want {
		tables = append(tables, t)
	}
	for t := range nonEmpty {
		if _, ok := want[t]; !ok {
			tables = append(tables, t)
		}
	}
	if len(tables) == 0 {
		return nil
	}
	sort.Strings(tables)
	var sb strings.Builder
	for i, t := range tables {
		if i > 0 {
			sb.WriteString(" UNION ALL ")
		}
		schema, name, _ := strings.Cut(t, ".")
		sb.WriteString(fmt.Sprintf(`SELECT '%s|' || count(*) FROM %s.%s`, strings.ReplaceAll(t, "'", "''"),
			quoteIdent(schema), quoteIdent(name)))
	}
	rows, err := query(ctx, sb.String())
	if err != nil {
		return fmt.Errorf("count the restored rows: %w", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		if t, n, ok := strings.Cut(r, "|"); ok {
			got[t], _ = strconv.ParseInt(n, 10, 64)
		}
	}
	for _, t := range tables {
		if n, ok := want[t]; ok && got[t] != n {
			m.Rows = append(m.Rows, fmt.Sprintf("%s: %d of %d rows", t, got[t], n))
		} else if !ok && got[t] == 0 {
			m.Rows = append(m.Rows, fmt.Sprintf("%s: none of the rows of its chunks", t))
		}
	}
	return nil
}
