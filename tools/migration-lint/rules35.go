package main

// Epic 35 story 35.5 AC5: the migration rules automatic updates rest on, as checks.
//
//  5. NO TRANSACTION idempotency: a file goose runs without a transaction (CREATE INDEX
//     CONCURRENTLY needs it) that a power loss cuts part way is marked unapplied and run
//     again whole (EC3), so every statement of its Up section must be harmless twice:
//     IF NOT EXISTS / IF EXISTS / CREATE OR REPLACE, an INSERT … ON CONFLICT (or WHERE NOT
//     EXISTS), a constraint added again after a DROP CONSTRAINT IF EXISTS, an UPDATE whose
//     values do not read the columns they set (x = x + 1 is not), or a statement that is so by
//     nature (DELETE, GRANT, COMMENT, a DO block, a SELECT, …); a WITH is judged by what it
//     writes.
//  6. MANIFEST markers match the function: a migration that (re)defines
//     maktaba_min_node_schema() has a MANIFEST row saying "Breaking for older nodes" with
//     the value the function returns, and every such row a migration that defines it
//     (story 35.1's min_node_schema and the nodes' schema guard read the two).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	reGooseUp   = regexp.MustCompile(`(?im)^\s*--\s*\+goose\s+Up\b`)
	reGooseDown = regexp.MustCompile(`(?im)^\s*--\s*\+goose\s+Down\b`)
	reNoTx      = regexp.MustCompile(`(?im)^\s*--\s*\+goose\s+NO\s+TRANSACTION\b`)

	// statements harmless twice by nature (an UPDATE and a WITH are judged by what they write)
	reNatural = regexp.MustCompile(`(?is)^\s*(?:SELECT|DELETE|GRANT|REVOKE|COMMENT\s+ON|ANALYZE|VACUUM|REINDEX|` +
		`REFRESH\s+MATERIALIZED\s+VIEW|SET|RESET|DO|NOTIFY|LOCK)\b`)
	reWith      = regexp.MustCompile(`(?is)^\s*WITH\b`)
	reUpdate    = regexp.MustCompile(`(?is)\bUPDATE\s+(?:ONLY\s+)?(\S+)(?:\s+(?:AS\s+)?(\w+))?\s+SET\b`)
	reInsertAny = regexp.MustCompile(`(?is)\bINSERT\s+INTO\b`)
	reSetEnd    = regexp.MustCompile(`(?is)\b(?:WHERE|FROM|RETURNING)\b`)
	reSameTwice = regexp.MustCompile(`(?i)\b(?:coalesce|greatest|least|lower|upper|trim|btrim|nullif)\s*\(`)
	reGuarded   = regexp.MustCompile(`(?is)^\s*(?:CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?IF\s+NOT\s+EXISTS|` +
		`CREATE\s+(?:(?:UNLOGGED|TEMP|TEMPORARY)\s+)?(?:TABLE|SCHEMA|SEQUENCE|EXTENSION|VIEW|MATERIALIZED\s+VIEW)\s+IF\s+NOT\s+EXISTS|` +
		`CREATE\s+OR\s+REPLACE|DROP\s+[A-Z ]+?\s+IF\s+EXISTS|` +
		`ALTER\s+(?:TABLE|INDEX|SEQUENCE|VIEW)\s+IF\s+EXISTS)\b`)
	reInsert      = regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\b`)
	reInsertSafe  = regexp.MustCompile(`(?is)\bON\s+CONFLICT\b|\bNOT\s+EXISTS\s*\(`)
	reAlterTable  = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(?:ONLY\s+)?\S+\s+(.*)$`)
	reActionGuard = regexp.MustCompile(`(?is)^(?:ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS|DROP\s+(?:COLUMN|CONSTRAINT)\s+IF\s+EXISTS|` +
		`ALTER\s+(?:COLUMN\s+)?\S+\s+(?:SET|DROP)\s+(?:DEFAULT|NOT\s+NULL)|ALTER\s+(?:COLUMN\s+)?\S+\s+SET\s+(?:STATISTICS|STORAGE)|` +
		`ENABLE|DISABLE|SET|RESET|OWNER\s+TO|REPLICA\s+IDENTITY|VALIDATE\s+CONSTRAINT)\b`)
	reDropConstr   = regexp.MustCompile(`(?is)\bDROP\s+CONSTRAINT\s+IF\s+EXISTS\s+("?[A-Za-z0-9_]+"?)`)
	reAddConstr    = regexp.MustCompile(`(?is)^ADD\s+CONSTRAINT\s+("?[A-Za-z0-9_]+"?)`)
	reDollarTag    = regexp.MustCompile(`^\$[A-Za-z_]*\$`)
	reFunctionDef  = regexp.MustCompile(`(?is)\bFUNCTION\s+maktaba_min_node_schema\s*\(\s*\)`)
	reFunctionBody = regexp.MustCompile(`(?is)FUNCTION\s+maktaba_min_node_schema\s*\(\s*\).*?\bSELECT\s+(\d+)`)
	reManifestRow  = regexp.MustCompile("^\\|\\s*`(\\d{4})`\\s*\\|")
	reManifestVal  = regexp.MustCompile(`maktaba_min_node_schema\(\)` + "`?" + `\s+returns\s+(\d+)`)
)

// slotOf is a migration file's slot (0 when its name has none).
func slotOf(file string) int {
	n, _ := strconv.Atoi(strings.SplitN(filepath.Base(file), "_", 2)[0])
	return n
}

// upSection is the part of a migration goose runs on the way up (all of it when it
// names no sections).
func upSection(body []byte) []byte {
	if loc := reGooseUp.FindIndex(body); loc != nil {
		body = body[loc[1]:]
	}
	if loc := reGooseDown.FindIndex(body); loc != nil {
		body = body[:loc[0]]
	}
	return body
}

// noTxCheck is rule 5 for one Postgres file.
func noTxCheck(file string, body []byte) []violation {
	if strings.HasSuffix(file, ".sqlite.sql") || !reNoTx.Match(body) {
		return nil
	}
	var out []violation
	dropped := map[string]bool{} // constraints dropped IF EXISTS so far: adding them again is harmless
	for _, st := range splitSQL(stripComments(upSection(body))) {
		for _, m := range reDropConstr.FindAllStringSubmatch(st, -1) {
			dropped[strings.Trim(m[1], `"`)] = true
		}
		if why := notIdempotent(st, dropped); why != "" {
			out = append(out, violation{File: file, Rule: "no-transaction-idempotent",
				Message: fmt.Sprintf("a NO TRANSACTION file is run again whole after a crash (story 35.5 AC5): %s — "+
					"statement: %q", why, oneLine(st, 100))})
		}
	}
	return out
}

// notIdempotent says why a statement is not harmless twice, "" when it is (dropped: the
// constraints dropped IF EXISTS before it, which it may add again).
func notIdempotent(st string, dropped map[string]bool) string {
	switch {
	case reNatural.MatchString(st), reGuarded.MatchString(st):
		return ""
	case reWith.MatchString(st):
		// what the WITH writes: an INSERT and an UPDATE are judged as alone
		if reInsertAny.MatchString(st) && !reInsertSafe.MatchString(st) {
			return "a WITH that inserts needs ON CONFLICT … DO NOTHING (or a WHERE NOT EXISTS of the row)"
		}
		if loc := reUpdate.FindStringIndex(st); loc != nil {
			return updateNotIdempotent(st[loc[0]:])
		}
		return ""
	case reUpdate.MatchString(st) && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(st)), "UPDATE"):
		return updateNotIdempotent(st)
	case reInsert.MatchString(st):
		if reInsertSafe.MatchString(st) {
			return ""
		}
		return "an INSERT needs ON CONFLICT … DO NOTHING (or a WHERE NOT EXISTS of the row)"
	}
	if m := reAlterTable.FindStringSubmatch(st); m != nil {
		for _, action := range splitActions(m[1]) {
			if c := reAddConstr.FindStringSubmatch(action); c != nil && dropped[strings.Trim(c[1], `"`)] {
				continue // dropped IF EXISTS first
			}
			if !reActionGuard.MatchString(action) {
				return "an ALTER TABLE action needs IF [NOT] EXISTS, or a DO block that checks first (" + oneLine(action, 60) + ")"
			}
		}
		return ""
	}
	return "use IF NOT EXISTS, IF EXISTS or CREATE OR REPLACE (or a DO block that checks first)"
}

// splitActions splits an ALTER TABLE's comma-separated actions (outside parentheses).
func splitActions(s string) []string {
	var (
		out   []string
		depth int
		start int
	)
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// splitSQL splits a script into statements at semicolons outside quotes: single-quoted
// strings, double-quoted names and dollar-quoted bodies ($$ … $$, $f$ … $f$), so a
// function's body is one statement.
func splitSQL(body []byte) []string {
	var (
		out   []string
		start int
		quote byte   // ' or " while inside one
		tag   string // the dollar quote's tag while inside a body
	)
	b := string(body)
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case tag != "":
			if c == '$' && strings.HasPrefix(b[i:], tag) {
				i += len(tag) - 1
				tag = ""
			}
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '$':
			if m := reDollarTag.FindString(b[i:]); m != "" {
				tag = m
				i += len(m) - 1
			}
		case c == ';':
			if st := strings.TrimSpace(b[start:i]); st != "" {
				out = append(out, st)
			}
			start = i + 1
		}
	}
	if st := strings.TrimSpace(b[start:]); st != "" {
		out = append(out, st)
	}
	return out
}

// manifestCheck is rule 6 over the migrations of dir and its MANIFEST.md.
func manifestCheck(dir string, files []string) []violation {
	md, err := os.ReadFile(filepath.Join(dir, "MANIFEST.md"))
	if err != nil {
		return []violation{{File: filepath.Join(dir, "MANIFEST.md"), Rule: "manifest-markers", Message: err.Error()}}
	}
	rows := map[int]int{} // slot -> the value its "Breaking for older nodes" row names
	for _, line := range strings.Split(string(md), "\n") {
		m := reManifestRow.FindStringSubmatch(line)
		if m == nil || !strings.Contains(line, "Breaking for older nodes") {
			continue
		}
		slot, _ := strconv.Atoi(m[1])
		v := slot
		if r := reManifestVal.FindStringSubmatch(line); r != nil {
			v, _ = strconv.Atoi(r[1])
		}
		rows[slot] = v
	}
	var out []violation
	defined := map[int]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, ".sqlite.sql") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		up := stripComments(upSection(body))
		if !reFunctionDef.Match(up) {
			continue
		}
		slot := slotOf(f)
		defined[slot] = true
		want, ok := rows[slot]
		if !ok {
			out = append(out, violation{File: f, Rule: "manifest-markers", Message: "defines maktaba_min_node_schema() but " +
				"its MANIFEST row does not say \"Breaking for older nodes\""})
			continue
		}
		if m := reFunctionBody.FindSubmatch(up); m != nil {
			if got, _ := strconv.Atoi(string(m[1])); got != want {
				out = append(out, violation{File: f, Rule: "manifest-markers", Message: fmt.Sprintf(
					"maktaba_min_node_schema() returns %d here, its MANIFEST row says %d", got, want)})
			}
		}
	}
	for slot := range rows {
		if !defined[slot] {
			out = append(out, violation{File: filepath.Join(dir, "MANIFEST.md"), Rule: "manifest-markers", Message: fmt.Sprintf(
				"slot %04d says \"Breaking for older nodes\" but its migration does not define maktaba_min_node_schema()", slot)})
		}
	}
	return out
}

// updateNotIdempotent says why an UPDATE is not harmless twice ("" when it is): a value
// that reads the column it sets (x = x + 1, x = x || 'a') changes it again, unless it reads
// it through a function that gives the same answer twice (COALESCE(x, …), GREATEST,
// LEAST, LOWER, UPPER, TRIM, NULLIF).
func updateNotIdempotent(st string) string {
	loc := reUpdate.FindStringSubmatchIndex(st)
	if loc == nil {
		return ""
	}
	// the column read as itself: unqualified, or through the updated table's name or alias
	// (another table's column of the same name, v.library_id, is no read of it)
	names := []string{regexp.QuoteMeta(st[loc[2]:loc[3]])}
	if loc[4] >= 0 {
		names = append(names, regexp.QuoteMeta(st[loc[4]:loc[5]]))
	}
	self := `(?:` + strings.Join(names, "|") + `)\.`
	set := withoutStrings(st[loc[1]:])
	if end := reSetEnd.FindStringIndex(set); end != nil {
		set = set[:end[0]]
	}
	for _, a := range splitActions(set) {
		col, value, ok := strings.Cut(a, "=")
		col = strings.Trim(strings.TrimSpace(col), `"`)
		if !ok || col == "" || strings.ContainsAny(col, "(), ") {
			continue // (a, b) = (…): not judged
		}
		reads := regexp.MustCompile(`(?i)(?:^|[^.\w])(?:` + self + `)?` + regexp.QuoteMeta(col) + `\b`)
		if reads.MatchString(withoutSameTwice(value)) {
			return "an UPDATE whose value reads the column it sets (" + oneLine(a, 60) + ") changes it again " +
				"when run twice: set it from other columns or constants, or guard it in the WHERE"
		}
	}
	return ""
}

// withoutStrings blanks single-quoted literals (a column's name inside one is no read).
func withoutStrings(s string) string {
	var b strings.Builder
	in := false
	for _, c := range s {
		switch {
		case c == '\'':
			in = !in
			b.WriteRune(c)
		case in:
			b.WriteRune(' ')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// withoutSameTwice removes the calls that give the same answer twice (COALESCE(…) and the
// like) with their arguments.
func withoutSameTwice(s string) string {
	for {
		loc := reSameTwice.FindStringIndex(s)
		if loc == nil {
			return s
		}
		depth, end := 0, len(s)
		for i := loc[1] - 1; i < len(s); i++ {
			if s[i] == '(' {
				depth++
			} else if s[i] == ')' {
				depth--
				if depth == 0 {
					end = i + 1
					break
				}
			}
		}
		s = s[:loc[0]] + "f()" + s[end:]
	}
}
