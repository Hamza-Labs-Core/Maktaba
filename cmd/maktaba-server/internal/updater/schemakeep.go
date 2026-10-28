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
// run on, or what a dump would be needed to undo.
var destructive = []*regexp.Regexp{
	// (not ALTER PUBLICATION ... DROP TABLE: a table leaving a machine's copy)
	regexp.MustCompile(`(?is)\bDROP\s+(TABLE|COLUMN|SCHEMA|SEQUENCE)\b`),
	regexp.MustCompile(`(?is)\bRENAME\b`),
	regexp.MustCompile(`(?is)\bALTER\s+COLUMN\s+\S+\s+(SET\s+DATA\s+)?TYPE\b`),
	regexp.MustCompile(`(?is)\bSET\s+NOT\s+NULL\b`),
	// a TRUNCATE statement (not a trigger's AFTER TRUNCATE, a grant's or a TG_OP's word)
	regexp.MustCompile(`(?im)(^|;)\s*TRUNCATE\s+(TABLE\s+)?(ONLY\s+)?["A-Za-z_]`),
}

// dropped are the drops of objects a migration may make again in the same file (a
// trigger, a function, a view, a type dropped and created anew): destructive only when the
// file does not create them again.
var dropped = regexp.MustCompile(`(?is)\bDROP\s+(FUNCTION|VIEW|MATERIALIZED\s+VIEW|TRIGGER|TYPE)\s+(IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)

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
	for _, re := range destructive {
		if m := re.FindString(body); m != "" {
			return "is destructive (" + strings.Join(strings.Fields(m), " ") + ")"
		}
	}
	for _, m := range dropped.FindAllStringSubmatch(body, -1) {
		kind, name := strings.Join(strings.Fields(strings.ToUpper(m[1])), " "), strings.Trim(m[3], `"`)
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		again := regexp.MustCompile(`(?is)\bCREATE\s+(OR\s+REPLACE\s+)?` + strings.ReplaceAll(regexp.QuoteMeta(kind), ` `, `\s+`) +
			`\s+(IF\s+NOT\s+EXISTS\s+)?("?[A-Za-z0-9_]+"?\.)?"?` + regexp.QuoteMeta(name) + `"?\b`)
		if !again.MatchString(body) {
			return fmt.Sprintf("is destructive (DROP %s %s)", kind, name)
		}
	}
	return ""
}
