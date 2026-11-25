package dbsafe

import (
	"bytes"
	"fmt"
	"strings"
)

// copyScan reads a pg_restore script with the tables and their rows as it goes to psql: the COPY blocks
// in it, and whether the last one ended. A table's rows end in a line of `\.`, which is in the
// dump's own file of them. Of a file cut short, which pg_restore does not always notice
// (PostgreSQL 18's ends well), it is not there, and psql takes what comes next for rows: the
// next tables' statements, up to the end of the script, or the COMMIT. A script whose COPY
// blocks are not the dump's tables, or whose last one never ended, is not run to its COMMIT.
type copyScan struct {
	copies int
	inCopy bool
	named  bool   // pg_restore said a table's rows come next
	line   []byte // the line so far, its first scanLine bytes
}

// scanLine is as much of a line as tells what it is (a COPY's names a table and its columns).
const scanLine = 1 << 20

func (s *copyScan) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if room := scanLine - len(s.line); room > 0 {
			s.line = append(s.line, part[:min(len(part), room)]...)
		}
		if i < 0 {
			break
		}
		s.end(bytes.TrimSuffix(s.line, []byte("\r")))
		s.line = s.line[:0]
		p = p[i+1:]
	}
	return n, nil
}

// end is a whole line.
func (s *copyScan) end(line []byte) {
	switch {
	case s.inCopy:
		s.inCopy = !bytes.Equal(line, []byte(`\.`))
	case bytes.HasPrefix(line, []byte("-- Data for Name: ")) && bytes.Contains(line, []byte("; Type: TABLE DATA; ")):
		s.named = true // not in a function's text, which may hold a COPY of its own
	case s.named && bytes.HasPrefix(line, []byte("COPY ")) && bytes.HasSuffix(line, []byte(" FROM stdin;")):
		s.copies, s.inCopy, s.named = s.copies+1, true, false
	}
}

// whole says what is wrong with a script that should hold the rows of tables tables.
func (s *copyScan) whole(tables int) error {
	if s.inCopy {
		return fmt.Errorf("the rows of a table are cut short (the dump is damaged)")
	}
	if s.copies != tables {
		return fmt.Errorf("it holds the rows of %d tables, not %d: some are cut short (the dump is damaged)", s.copies, tables)
	}
	return nil
}

// withoutItems is list (pg_restore -l's) without the items named (their kind, schema and name:
// "EXTENSION - vector"): commented out, as pg_restore's --use-list wants.
func withoutItems(list string, skip map[string]bool) string {
	lines := strings.Split(list, "\n")
	for i, line := range lines {
		_, rest, ok := strings.Cut(line, "; ")
		f := strings.SplitN(rest, " ", 3) // the catalog's and the object's oids, then the rest
		if !ok || strings.HasPrefix(line, ";") || len(f) < 3 {
			continue
		}
		item := strings.TrimRight(f[2], " \r")
		at := strings.LastIndex(item, " ") // its owner last, when it has one
		if skip[item] || (at > 0 && skip[item[:at]]) {
			lines[i] = ";" + line
		}
	}
	return strings.Join(lines, "\n")
}
