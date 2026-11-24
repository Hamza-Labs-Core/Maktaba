package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

// A deploy that fails after its migrations goes back with their Down scripts
// (docs/cloud-single-node.md, "When a deploy fails"): Undo runs those of exactly the versions
// the deploy applied, newest first, and only when that loses nothing. It checks every one before
// it runs the first; the refusals change nothing.
var (
	// ErrUndoLossy: a migration's Up lost data (or says it is a contract), or it has no Down
	// script: no Down script can give back what the release before needs.
	ErrUndoLossy = errors.New("migrate undo: a migration cannot be undone")
	// ErrUndoKeeps: a Down script would delete rows written since the migration ran. The schema is
	// better kept: a release's migrations only expand it (story 50.08), so the release before runs
	// on it.
	ErrUndoKeeps = errors.New("migrate undo: a Down script would delete data written since")
)

// Applied is the versions the database records as applied, in the order they were.
func (m *Migrator) Applied(ctx context.Context) ([]int64, error) {
	var there bool
	if err := m.pool.DB.QueryRowContext(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&there); err != nil || !there {
		return nil, err
	}
	rows, err := m.pool.DB.QueryContext(ctx, `SELECT version_id FROM goose_db_version WHERE is_applied AND version_id > 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Undo runs the Down scripts of versions, the newest applied first. ErrUndoLossy and ErrUndoKeeps
// come back before any ran; another error may come after some did.
func (m *Migrator) Undo(ctx context.Context, versions []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	unlock, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	applied, err := m.Applied(ctx)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, v := range versions {
		want[v] = true
	}
	var order []int64 // the last applied first
	for i := len(applied) - 1; i >= 0; i-- {
		if want[applied[i]] {
			order = append(order, applied[i])
			delete(want, applied[i])
		}
	}
	for v := range want {
		return fmt.Errorf("migrate undo: version %d is not applied", v)
	}
	sub, err := fs.Sub(m.dir, m.root)
	if err != nil {
		return err
	}
	// every one is judged before any refusal is given, the gravest first: one that can't be undone
	// must not hide behind another that is only better kept (the previous release would be started
	// on a schema it can't run on), nor behind one that could not be read
	var lossy, keeps, unread []string
	for _, v := range order {
		name, text, err := migrationFile(sub, v)
		if err != nil {
			lossy = append(lossy, err.Error())
			continue
		}
		verdict, err := m.Judge(ctx, text)
		switch {
		case err != nil:
			unread = append(unread, name+": "+err.Error())
		case errors.Is(verdict.Err, ErrUndoLossy):
			lossy = append(lossy, name+" "+verdict.Why)
		case verdict.Why != "":
			keeps = append(keeps, name+" "+verdict.Why)
		}
	}
	switch {
	case len(lossy) > 0:
		return fmt.Errorf("%w: %s", ErrUndoLossy, strings.Join(lossy, "; "))
	case len(unread) > 0:
		return fmt.Errorf("migrate undo: reading what a Down script would delete: %s", strings.Join(unread, "; "))
	case len(keeps) > 0:
		return fmt.Errorf("%w: %s", ErrUndoKeeps, strings.Join(keeps, "; "))
	}
	p, err := goose.NewProvider(goose.DialectPostgres, m.pool.DB, sub, goose.WithAllowOutofOrder(true))
	if err != nil {
		return err
	}
	for _, v := range order {
		if _, err := p.ApplyVersion(ctx, v, false); err != nil {
			return fmt.Errorf("migrate undo: version %d: %w", v, err)
		}
	}
	return nil
}

// Verdict is whether a migration's Down script may run: Why not ("" it may), and the refusal it is.
type Verdict struct {
	Err error
	Why string
}

func (v Verdict) String() string {
	switch {
	case v.Why == "":
		return "can be undone"
	case errors.Is(v.Err, ErrUndoKeeps):
		return "kept: " + v.Why
	default:
		return "cannot be undone: " + v.Why
	}
}

// Judge reads a migration's text, and what its Down script would delete as the database is now.
func (m *Migrator) Judge(ctx context.Context, text string) (Verdict, error) {
	up, down := migrationSections(text)
	if strings.TrimSpace(down) == "" {
		return Verdict{ErrUndoLossy, "has no Down script"}, nil
	}
	if what := upLoses(text, up); what != "" {
		return Verdict{ErrUndoLossy, what}, nil
	}
	what, err := m.downLoses(ctx, down)
	if err != nil || what == "" {
		return Verdict{}, err
	}
	return Verdict{ErrUndoKeeps, what}, nil
}

// migrationFile is version's file.
func migrationFile(dir fs.FS, version int64) (name, text string, err error) {
	ents, err := fs.ReadDir(dir, ".")
	if err != nil {
		return "", "", err
	}
	for _, e := range ents {
		n, _, ok := strings.Cut(e.Name(), "_")
		if !ok || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if v, perr := strconv.ParseInt(n, 10, 64); perr == nil && v == version {
			b, err := fs.ReadFile(dir, e.Name())
			return e.Name(), string(b), err
		}
	}
	return "", "", fmt.Errorf("this release has no migration %d", version)
}

// migrationSections are a migration's Up and Down scripts, without comments, goose's directives
// and dollar-quoted bodies (a function's body is defined, not run).
func migrationSections(text string) (up, down string) {
	up = text
	if i := strings.Index(up, "+goose Down"); i >= 0 {
		up, down = up[:i], up[i+len("+goose Down"):]
	}
	if i := strings.Index(up, "+goose Up"); i >= 0 {
		up = up[i+len("+goose Up"):]
	}
	return withoutBodies(withoutComments(up)), withoutBodies(withoutComments(down))
}

var blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func withoutComments(s string) string {
	var b strings.Builder
	s = blockComment.ReplaceAllString(s, " ")
	for _, l := range strings.Split(s, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

var dollarTag = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)?\$`)

func withoutBodies(s string) string {
	var b strings.Builder
	for {
		loc := dollarTag.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String()
		}
		tag := s[loc[0]:loc[1]]
		end := strings.Index(s[loc[1]:], tag)
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:loc[0]] + "''")
		s = s[loc[1]+end+len(tag):]
	}
}

var (
	contractMark = regexp.MustCompile(`(?im)^\s*--\s*contract:`)
	downRestores = regexp.MustCompile(`(?im)^\s*--\s*\+maktaba\s+down-restores:\s*\S`)
	// anywhere in a statement (WITH x AS (DELETE ...) deletes too); ON CONFLICT DO UPDATE SET and a
	// foreign key's ON UPDATE SET NULL name no table after UPDATE
	rowsLost = regexp.MustCompile(`(?is)\b(TRUNCATE\b|DELETE\s+FROM\b|MERGE\s+INTO\b|UPDATE\s+(?:ONLY\s+)?["A-Za-z_][A-Za-z0-9_."]*(?:\s+(?:AS\s+)?[A-Za-z_][A-Za-z0-9_]*)?\s+SET\b)`)
	// what a DO block could do is not read: it runs, so it counts as losing data
	doBlock    = regexp.MustCompile(`(?is)(^|;)\s*DO\b`)
	typeRecast = regexp.MustCompile(`(?is)\bALTER\s+(COLUMN\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+(SET\s+DATA\s+)?TYPE\b`)
	dropTable  = regexp.MustCompile(`(?is)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([^;]+)`)
	alterTable = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?([A-Za-z_"][A-Za-z0-9_."]*)\s+([^;]+)`)
	dropColumn = regexp.MustCompile(`(?is)\bDROP\s+(COLUMN\s+)?(?:IF\s+EXISTS\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)
	otherDrop  = regexp.MustCompile(`(?is)\bDROP\s+(SCHEMA|SEQUENCE|MATERIALIZED|DATABASE)\b`)
)

// keptByDrop are the words after DROP, inside an ALTER TABLE, that lose no rows.
var keptByDrop = map[string]bool{"constraint": true, "default": true, "not": true, "identity": true, "expression": true}

// dropped is what a script drops: tables, and each table's columns.
func dropped(script string) (tables []string, columns [][2]string) {
	for _, m := range dropTable.FindAllStringSubmatch(script, -1) {
		list := regexp.MustCompile(`(?i)\s+(CASCADE|RESTRICT)\s*$`).ReplaceAllString(strings.TrimSpace(m[1]), "")
		for _, t := range strings.Split(list, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tables = append(tables, t)
			}
		}
	}
	for _, m := range alterTable.FindAllStringSubmatch(script, -1) {
		for _, d := range dropColumn.FindAllStringSubmatch(m[2], -1) {
			if d[1] == "" && keptByDrop[strings.ToLower(d[2])] {
				continue
			}
			columns = append(columns, [2]string{strings.TrimSpace(m[1]), d[2]})
		}
	}
	return tables, columns
}

// upLoses says how a migration's Up lost data no Down script gives back ("" it did not).
func upLoses(text, up string) string {
	if contractMark.MatchString(text) {
		return "is a contract migration"
	}
	if downRestores.MatchString(text) {
		return ""
	}
	if doBlock.MatchString(up) {
		return "runs a DO block, whose effect is not known"
	}
	if m := rowsLost.FindString(up); m != "" {
		return "changed or deleted rows (" + strings.Join(strings.Fields(m), " ") + ")"
	}
	if m := typeRecast.FindString(up); m != "" {
		return "recast a column"
	}
	if m := otherDrop.FindString(up); m != "" {
		return "dropped data (" + m + ")"
	}
	if t, c := dropped(up); len(t) > 0 {
		return "dropped the table " + t[0]
	} else if len(c) > 0 {
		return "dropped the column " + c[0][0] + "." + c[0][1]
	}
	return ""
}

// downLoses says what data a Down script would delete as the database is now ("" none): rows of a
// table it drops, values other than the default in a column it drops, or rows it changes itself.
func (m *Migrator) downLoses(ctx context.Context, down string) (string, error) {
	if x := rowsLost.FindString(down); x != "" {
		return "changes or deletes rows (" + strings.Join(strings.Fields(x), " ") + ")", nil
	}
	if doBlock.MatchString(down) {
		return "runs a DO block, whose effect is not known", nil
	}
	if typeRecast.MatchString(down) {
		return "recasts a column", nil
	}
	if x := otherDrop.FindString(down); x != "" {
		return "drops more than a table or a column (" + x + ")", nil
	}
	tables, columns := dropped(down)
	for _, t := range tables {
		has, err := m.exists(ctx, t, `SELECT EXISTS (SELECT 1 FROM `+t+`)`)
		if err != nil {
			return "", err
		}
		if has {
			return "drops the table " + t + ", which holds rows", nil
		}
	}
	for _, c := range columns {
		var def sql.NullString
		err := m.pool.DB.QueryRowContext(ctx, `
            SELECT pg_get_expr(d.adbin, d.adrelid)
              FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
             WHERE a.attrelid = to_regclass($1) AND a.attname = $2 AND NOT a.attisdropped`,
			c[0], strings.Trim(c[1], `"`)).Scan(&def)
		if errors.Is(err, sql.ErrNoRows) {
			continue // the column is not there (DROP COLUMN IF EXISTS)
		}
		if err != nil {
			return "", err
		}
		d := "NULL"
		if def.Valid {
			d = "(" + def.String + ")"
		}
		has, err := m.exists(ctx, c[0], `SELECT EXISTS (SELECT 1 FROM `+c[0]+` WHERE (`+c[1]+`)::text IS DISTINCT FROM (`+d+`)::text)`)
		if err != nil {
			return "", err
		}
		if has {
			return "drops the column " + c[0] + "." + c[1] + ", which holds values", nil
		}
	}
	return "", nil
}

// exists runs q (an EXISTS over table) when the table is there.
func (m *Migrator) exists(ctx context.Context, table, q string) (bool, error) {
	var there, has bool
	if err := m.pool.DB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&there); err != nil || !there {
		return false, err
	}
	err := m.pool.DB.QueryRowContext(ctx, q).Scan(&has)
	return has, err
}
