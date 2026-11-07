package updater

// A rollback that undoes only what the release changed (the rollback of 0.3.90 on
// 2026-11-07: its one migration added two columns and two checks to user_playlists, and
// the rollback put the whole 73 GB database back from its dump instead, rebuilding every
// index for most of a day and losing the day's writes). Every migration has an Up script
// and a Down script (goose). When the schema cannot be kept (schemakeep.go) and every
// migration since the release before loses no data on its way up, the rollback runs their
// Down scripts back to the release before's schema (`maktaba-api migrate down-to`): only
// the tables they touched change, every other table, index and row stays. The dump goes
// back only when an Up script lost data a Down script cannot bring back, or a Down fails.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SchemaReverter is an installer that can tell whether its rollback may run the new
// release's Down scripts back to the release before's schema, and which schema that is.
type SchemaReverter interface {
	DownTo(j *Journal) (schema int64, ok bool, why string)
}

// DownRecords is a Records that can say the database is back on the release before's
// schema by its Down scripts (layout.MarkRecordDown): the release before starts on it as
// on its own.
type DownRecords interface {
	MarkDown(version, from, reason string) (bool, error)
}

// DownToFrom decides a rollback from the release whose migrations folder is to back to
// the one whose migrations folder is from: the Down scripts of to's migrations above
// from's newest may run when every one has a Down script and loses no data on its way up
// (Reversible). schema is from's newest migration.
func DownToFrom(from, to string) (schema int64, ok bool, why string) {
	if from == "" || to == "" {
		return 0, false, "the releases' migrations are not known"
	}
	fromSchema, toSchema := newestMigration(from), newestMigration(to)
	if fromSchema == 0 || toSchema == 0 {
		return 0, false, "the releases' schemas are not known"
	}
	ok, why = Reversible(to, fromSchema, toSchema)
	return fromSchema, ok, why
}

// lossy are the statements of an Up script whose change a Down script cannot undo: rows
// deleted or overwritten, a column's values recast. A DROP is judged by dropLosesData.
var lossy = []*regexp.Regexp{
	regexp.MustCompile(`(?im)(^|;)\s*TRUNCATE\b`),
	regexp.MustCompile(`(?im)(^|;)\s*DELETE\s+FROM\b`),
	regexp.MustCompile(`(?im)(^|;)\s*UPDATE\s+["A-Za-z_][^;]*\bSET\b`),
	regexp.MustCompile(`(?is)\bALTER\s+(COLUMN\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+(SET\s+DATA\s+)?TYPE\b`),
}

// keptByDrop are the words after DROP that lose no data: what a Down script makes again
// from its definition alone.
var keptByDrop = map[string]bool{"CONSTRAINT": true, "INDEX": true, "TRIGGER": true, "FUNCTION": true,
	"PROCEDURE": true, "VIEW": true, "POLICY": true, "DEFAULT": true, "NOT": true, "RULE": true}

// Reversible reports whether the migrations of the folder dir in the slots above
// fromSchema up to toSchema can be undone by their Down scripts with nothing lost: each
// has a Down script with a statement, and its Up script deletes, overwrites, recasts and
// drops no data (a table, a column, a sequence). why names the first that cannot.
func Reversible(dir string, fromSchema, toSchema int64) (bool, string) {
	if toSchema <= fromSchema {
		return false, "no migration since the release before"
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
		up, down := sections(string(b))
		if strings.TrimSpace(down) == "" {
			return false, name + " has no Down script"
		}
		if what := losesData(up); what != "" {
			return false, name + " " + what
		}
		n++
	}
	if n == 0 {
		return false, "no migration file since the release before"
	}
	return true, fmt.Sprintf("the %d migrations since the release before lose no data: their Down scripts undo them", n)
}

// sections are a migration's Up and Down scripts without their comments and goose's
// directives.
func sections(sql string) (up, down string) {
	code := func(s string) string {
		var b strings.Builder
		for _, l := range strings.Split(s, "\n") {
			if i := strings.Index(l, "--"); i >= 0 {
				l = l[:i]
			}
			b.WriteString(l + "\n")
		}
		return b.String()
	}
	u, d := sql, ""
	if i := strings.Index(u, "+goose Down"); i >= 0 {
		u, d = u[:i], u[i+len("+goose Down"):]
	}
	if i := strings.Index(u, "+goose Up"); i >= 0 {
		u = u[i+len("+goose Up"):]
	}
	return code(u), code(d)
}

// losesData is the statement of an Up script that loses data ("" none).
func losesData(up string) string {
	said := func(m string) string { return "loses data (" + strings.Join(strings.Fields(m), " ") + ")" }
	for _, re := range lossy {
		if m := re.FindString(up); m != "" {
			return said(strings.TrimLeft(m, ";\n\t "))
		}
	}
	for _, loc := range dropWord.FindAllStringSubmatchIndex(up, -1) {
		word := strings.ToUpper(strings.Trim(up[loc[2]:loc[3]], `"`))
		if keptByDrop[word] {
			continue
		}
		rest := up[loc[0]:]
		return said(strings.SplitN(rest[:min(len(rest), loc[3]-loc[0]+40)], ";", 2)[0])
	}
	return ""
}

// migrateDown runs the new release's Down scripts back to the release before's schema
// when the rollback may (SchemaReverter, DownRecords, MigrateDown), and marks the record
// so the release before starts on it. It reports whether the database is back that way;
// otherwise the dump goes back, as before.
func (m *Machine) migrateDown(j *Journal, reason string) bool {
	rv, ok := m.Installer.(SchemaReverter)
	dr, ok2 := m.Records.(DownRecords)
	if !ok || !ok2 || m.MigrateDown == nil || j.From == "" {
		return false
	}
	schema, safe, why := rv.DownTo(j)
	if !safe {
		m.logf("%s's Down scripts cannot undo it: %s", j.Job.Version, why)
		return false
	}
	m.logf("undoing %s's migrations with their Down scripts, back to slot %04d (%s): nothing else in the "+
		"database changes", j.Job.Version, schema, why)
	ctx, cancel := context.WithCancel(m.ctx())
	defer cancel()
	if err := m.MigrateDown(ctx, j, schema); err != nil {
		m.errorf("the Down scripts of %s failed (%v): the dump goes back instead", j.Job.Version, err)
		return false
	}
	marked, err := dr.MarkDown(j.Job.Version, j.From, reason)
	if err != nil || !marked {
		m.errorf("the database is on %s's schema, but its record cannot say so (%v): the dump goes back instead",
			orNone(j.From), err)
		return false
	}
	m.logf("the database is back on %s's schema by %s's Down scripts: %s starts on it, nothing is put back",
		orNone(j.From), j.Job.Version, orNone(j.From))
	return true
}
