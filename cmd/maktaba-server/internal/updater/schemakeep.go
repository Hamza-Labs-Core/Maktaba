package updater

// A rollback that keeps the schema (the outage of 2026-10-28: a release whose one
// migration added a row rolled back by putting the whole database back from its dump, and
// that restore failed). The migrations only move forward and are additive by rule
// (shared/db/migrations/README.md, rules 9 and 10; `make compat-prev` runs the previous
// release's tests on the new schema): the release before runs on the database the new
// one's migrations made. So a rollback puts the dump back only when a migration since the
// release before is not rollback-safe: one its MANIFEST row marks "Breaking for the
// previous release" or "Breaking for older nodes", or whose Up section drops, renames,
// retypes or tightens. Otherwise the database stays as it is (the extra tables, columns and
// rows in place) and the record says the release before may start on it
// (layout.MarkRecordKept), when that release says it does (its migrations'
// meta/schema-keep.json): one from before this rule refuses a newer schema, and its
// rollback still puts the dump back.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SchemaKeeper is an installer that can tell whether its rollback keeps the database's
// schema: the release before says it runs on a newer, rollback-safe schema, and every
// migration since it is rollback-safe. why says what decided it (the run's log).
type SchemaKeeper interface {
	KeepsSchema(j *Journal) (ok bool, why string)
}

// KeptRecords is a Records that can mark the record kept (a rollback with nothing put
// back): layout.MarkRecordKept.
type KeptRecords interface {
	MarkKept(version, from, reason string) (bool, error)
}

// SchemaKeepFile is the file in a release's migrations folder that says the release runs
// on a newer schema whose migrations are rollback-safe, when the database's record keeps
// it for it (protocol 1: serve's database step accepts the record's kept_for, and the api
// a schema ahead of its own with MAKTABA_SCHEMA_AHEAD_OK).
const SchemaKeepFile = "meta/schema-keep.json"

// SchemaKeepProtocol is the protocol this release speaks.
const SchemaKeepProtocol = 1

// keepsNewerSchema reports whether the release whose migrations folder is dir says it runs
// on a kept, newer schema.
func keepsNewerSchema(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(SchemaKeepFile)))
	if err != nil {
		return false
	}
	var f struct {
		Protocol int `json:"protocol"`
	}
	return json.Unmarshal(b, &f) == nil && f.Protocol >= 1
}

// KeepsSchemaFrom decides a rollback from the release whose migrations folder is to back
// to the one whose migrations folder is from: the schema is kept when from says it runs
// on one (keepsNewerSchema) and every migration of to above from's newest is
// rollback-safe (RollbackSafe).
func KeepsSchemaFrom(from, to string) (bool, string) {
	if from == "" || to == "" {
		return false, "the releases' migrations are not known"
	}
	if !keepsNewerSchema(from) {
		return false, "the release before does not run on a newer schema (it has no " + SchemaKeepFile + ")"
	}
	fromSchema, toSchema := newestMigration(from), newestMigration(to)
	if fromSchema == 0 || toSchema == 0 {
		return false, "the releases' schemas are not known"
	}
	return RollbackSafe(to, fromSchema, toSchema)
}

// manifestRow is a MANIFEST.md row: | `NNNN` | ...
var manifestRow = regexp.MustCompile("^\\|\\s*`(\\d{4,6})`\\s*\\|")

// Destructive statements of a migration's Up section: what the release before may not
// run on (a column, a default, an index, a constraint, a type it relies on gone; a
// constraint or a unique index its writes may break), or what a dump would be needed to
// undo. Anything uncertain counts as destructive: the cost is only a restore.
var destructive = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\bRENAME\b`),
	// a column's type changed, with or without the COLUMN keyword
	regexp.MustCompile(`(?is)\bALTER\s+(COLUMN\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+(SET\s+DATA\s+)?TYPE\b`),
	regexp.MustCompile(`(?is)\bSET\s+NOT\s+NULL\b`),
	// a constraint added (the release before may write what it refuses)
	regexp.MustCompile(`(?is)\bADD\s+(CONSTRAINT|CHECK|UNIQUE|PRIMARY\s+KEY|FOREIGN\s+KEY|EXCLUDE)\b`),
	regexp.MustCompile(`(?is)\bCREATE\s+UNIQUE\s+INDEX\b`),
	// a TRUNCATE statement (not a trigger's AFTER TRUNCATE, a grant's or a TG_OP's word)
	regexp.MustCompile(`(?im)(^|;)\s*TRUNCATE\s+(TABLE\s+)?(ONLY\s+)?["A-Za-z_]`),
}

// dropWord is every DROP of the Up section with the word after it (a column dropped
// without the COLUMN keyword: ALTER TABLE t DROP c). Only three are not destructive by
// themselves: DROP NOT NULL (it loosens), and a trigger or a function dropped and made
// again in the same file (the same trigger on the same table, the same signature).
var dropWord = regexp.MustCompile(`(?is)\bDROP\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)

// droppedTrigger and droppedFunction are those two drops.
var (
	droppedTrigger  = regexp.MustCompile(`(?is)^DROP\s+TRIGGER\s+(IF\s+EXISTS\s+)?("?[A-Za-z0-9_]+"?)\s+ON\s+([A-Za-z0-9_."]+)`)
	droppedFunction = regexp.MustCompile(`(?is)^DROP\s+FUNCTION\s+(IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)\s*\(`)
	// notNullNoDefault: a column added NOT NULL with no default (the release before's
	// inserts, which do not name it, fail)
	addColumn = regexp.MustCompile(`(?is)\bADD\s+(COLUMN\s+)?(IF\s+NOT\s+EXISTS\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+[^,;]*`)
)

// RollbackSafe reports whether the migrations of the folder dir in the slots above
// fromSchema up to toSchema leave a database the release at fromSchema runs on: none
// whose MANIFEST.md row says "Breaking for the previous release" or "Breaking for older
// nodes", and none whose Up section is destructive. why names the first that is not (or
// says they all are). No MANIFEST.md: not safe (nothing tells).
func RollbackSafe(dir string, fromSchema, toSchema int64) (bool, string) {
	if toSchema <= fromSchema {
		return true, "no migration since the release before"
	}
	md, err := os.ReadFile(filepath.Join(dir, "MANIFEST.md"))
	if err != nil {
		return false, "the migrations' MANIFEST.md cannot be read: " + err.Error()
	}
	for _, line := range strings.Split(string(md), "\n") {
		m := manifestRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slot, _ := strconv.ParseInt(m[1], 10, 64)
		if slot <= fromSchema || slot > toSchema {
			continue
		}
		for _, marker := range []string{"Breaking for the previous release", "Breaking for older nodes"} {
			if strings.Contains(line, marker) {
				return false, fmt.Sprintf("slot %04d is %q", slot, marker)
			}
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false, err.Error()
	}
	n := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sqlite.sql") {
			continue
		}
		s, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		slot, err := strconv.ParseInt(s, 10, 64)
		if err != nil || slot <= fromSchema || slot > toSchema {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return false, err.Error()
		}
		if what := destructiveIn(string(b)); what != "" {
			return false, fmt.Sprintf("%s %s", name, what)
		}
		n++
	}
	return true, fmt.Sprintf("the %d migrations since the release before are additive", n)
}

// destructiveIn is what makes a migration's Up section destructive ("" none).
func destructiveIn(sql string) string {
	up := sql
	if i := strings.Index(up, "+goose Up"); i >= 0 {
		up = up[i:]
	}
	if i := strings.Index(up, "+goose Down"); i >= 0 {
		up = up[:i]
	}
	// comments say what the migration does, in words a statement would use
	var code strings.Builder
	for _, l := range strings.Split(up, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		code.WriteString(l + "\n")
	}
	body := code.String()
	said := func(m string) string { return "is destructive (" + strings.Join(strings.Fields(m), " ") + ")" }
	for _, re := range destructive {
		if m := re.FindString(body); m != "" {
			return said(m)
		}
	}
	for _, loc := range dropWord.FindAllStringSubmatchIndex(body, -1) {
		word := strings.ToUpper(strings.Trim(body[loc[2]:loc[3]], `"`))
		rest := body[loc[0]:]
		switch word {
		case "NOT":
			if regexp.MustCompile(`(?is)^DROP\s+NOT\s+NULL\b`).MatchString(rest) {
				continue // loosens
			}
		case "TRIGGER":
			if m := droppedTrigger.FindStringSubmatch(rest); m != nil && triggerMadeAgain(body, m[2], m[3]) {
				continue
			}
		case "FUNCTION":
			if m := droppedFunction.FindStringSubmatchIndex(rest); m != nil {
				name := rest[m[4]:m[5]]
				if sig, ok := parenthesized(rest[m[1]-1:]); ok && functionMadeAgain(body, name, sig) {
					continue
				}
			}
		}
		end := min(len(rest), loc[3]-loc[0]+40)
		return said(strings.SplitN(rest[:end], ";", 2)[0])
	}
	for _, m := range addColumn.FindAllString(body, -1) {
		up := strings.ToUpper(m)
		if regexp.MustCompile(`\bNOT\s+NULL\b`).MatchString(up) && !strings.Contains(up, "DEFAULT") {
			return said(m) + ": NOT NULL with no default"
		}
		if regexp.MustCompile(`\b(REFERENCES|CHECK|UNIQUE|PRIMARY\s+KEY)\b`).MatchString(up) {
			return said(m) + ": a constraint"
		}
	}
	return ""
}

// unqualified is a name without its schema or quotes, in lower case.
func unqualified(name string) string {
	name = strings.ToLower(strings.ReplaceAll(name, `"`, ""))
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// triggerMadeAgain reports whether body creates the trigger name on table again.
func triggerMadeAgain(body, name, table string) bool {
	re := regexp.MustCompile(`(?is)\bCREATE\s+(OR\s+REPLACE\s+)?(CONSTRAINT\s+)?TRIGGER\s+"?` + regexp.QuoteMeta(unqualified(name)) +
		`"?\s[^;]*?\bON\s+([A-Za-z0-9_."]+)`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if unqualified(m[3]) == unqualified(table) {
			return true
		}
	}
	return false
}

// parenthesized is the text from s's opening parenthesis to its matching one (both
// included).
func parenthesized(s string) (string, bool) {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i+1], true
			}
		}
	}
	return "", false
}

// signature is a parameter list as it compares: lower case, no spaces or quotes.
func signature(params string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "\t", "", "\n", "", `"`, "").Replace(params))
}

// functionMadeAgain reports whether body creates the function name with exactly the
// parameter list sig again (another signature is another function: the release before's
// calls to the dropped one fail).
func functionMadeAgain(body, name, sig string) bool {
	re := regexp.MustCompile(`(?is)\bCREATE\s+(OR\s+REPLACE\s+)?FUNCTION\s+([A-Za-z0-9_."]+)\s*\(`)
	for _, loc := range re.FindAllStringSubmatchIndex(body, -1) {
		if unqualified(body[loc[4]:loc[5]]) != unqualified(name) {
			continue
		}
		if got, ok := parenthesized(body[loc[1]-1:]); ok && signature(got) == signature(sig) {
			return true
		}
	}
	return false
}
