package db

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
)

type backfillTable struct {
	name        string // SQL spelling, including schema qualification
	unqualified string // when used, must resolve to the same relation as name
	added       map[string]bool
	written     map[string]bool
}

// backfillColumns recognizes a narrow migration shape: add columns, fill them,
// then drop the additions on Down. Other shapes use the ordinary undo rules.
func backfillColumns(up, down [][]string) map[string]*backfillTable {
	tables := map[string]*backfillTable{}
	hasUpdate := false
	for _, stmt := range up {
		switch {
		case startsSQL(stmt, "alter", "table"):
			name, unqualified, rest := backfillRelation(stmt[2:])
			if name == "" {
				return nil
			}
			if tables[name] == nil {
				tables[name] = &backfillTable{name: name, added: map[string]bool{}, written: map[string]bool{}}
			}
			t := tables[name]
			if unqualified != "" {
				t.unqualified = unqualified
			}
			for _, action := range splitSQLList(rest, ",") {
				// IF NOT EXISTS cannot prove the column belongs to this migration.
				if !startsSQL(action, "add", "column") || len(action) < 4 || action[2] == "if" ||
					sqlIdentifier(action[2]) == "" || !backfillColumnDefinition(action[3:]) {
					return nil
				}
				t.added[sqlIdentifier(action[2])] = true
			}
		case startsSQL(stmt, "update"):
			name, unqualified, rest := backfillRelation(stmt[1:])
			t := tables[name]
			if t == nil || !backfillAssignments(rest, t, tables) {
				return nil
			}
			if unqualified != "" {
				t.unqualified = unqualified
			}
			hasUpdate = true
		default:
			return nil
		}
	}
	if !hasUpdate {
		return nil
	}

	drops := map[[2]string]bool{}
	for _, stmt := range down {
		if !startsSQL(stmt, "alter", "table") {
			return nil
		}
		name, unqualified, rest := backfillRelation(stmt[2:])
		t := tables[name]
		if t == nil {
			return nil
		}
		if unqualified != "" {
			t.unqualified = unqualified
		}
		for _, action := range splitSQLList(rest, ",") {
			if !startsSQL(action, "drop", "column") {
				return nil
			}
			action = action[2:]
			if startsSQL(action, "if", "exists") {
				action = action[2:]
			}
			if len(action) == 0 || (len(action) > 1 && !slices.Equal(action[1:], []string{"restrict"})) {
				return nil
			}
			col := sqlIdentifier(action[0])
			if !t.added[col] {
				return nil
			}
			drops[[2]string{name, col}] = true
		}
	}
	for name, t := range tables {
		for col := range t.added {
			if !drops[[2]string{name, col}] {
				return nil
			}
		}
	}
	return tables
}

func backfillAssignments(stmt []string, t *backfillTable, tables map[string]*backfillTable) bool {
	// UPDATE accepts an optional alias before SET.
	if startsSQL(stmt, "as") {
		stmt = stmt[1:]
	}
	if len(stmt) > 0 && stmt[0] != "set" && sqlIdentifier(stmt[0]) != "" {
		stmt = stmt[1:]
	}
	if !startsSQL(stmt, "set") || !backfillExpressions(stmt[1:]) {
		return false
	}
	assignments := stmt[1:]
	depth := 0
scan:
	for i, tok := range assignments {
		switch tok {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		case "from", "where":
			if tok == "from" && i > 0 && assignments[i-1] == "distinct" {
				continue
			}
			if depth == 0 {
				if tok == "from" && !backfillSources(assignments[i+1:], tables) {
					return false
				}
				assignments = assignments[:i]
				break scan
			}
		}
	}
	for _, assignment := range splitSQLList(assignments, ",") {
		eq := slices.Index(assignment, "=")
		if eq < 1 || eq == len(assignment)-1 {
			return false
		}
		lhs := assignment[:eq]
		if len(lhs) >= 3 && lhs[0] == "(" && lhs[len(lhs)-1] == ")" {
			lhs = lhs[1 : len(lhs)-1]
		}
		for _, column := range splitSQLList(lhs, ",") {
			if len(column) != 1 {
				return false
			}
			col := sqlIdentifier(column[0])
			if !t.added[col] {
				return false
			}
			t.written[col] = true
		}
	}
	return true
}

// A FROM source can hide a function in a view or foreign table. Identify
// each plain relation so judgeBackfill can verify its kind in the catalog.
func backfillSources(tokens []string, tables map[string]*backfillTable) bool {
	depth := 0
	for i, tok := range tokens {
		readRelation := i == 0 || (depth == 0 && (tokens[i-1] == "join" || tokens[i-1] == ","))
		if readRelation {
			name, unqualified, _ := backfillRelation(tokens[i:])
			if name == "" {
				return false
			}
			if tables[name] == nil {
				tables[name] = &backfillTable{name: name, added: map[string]bool{}, written: map[string]bool{}}
			}
			if unqualified != "" {
				tables[name].unqualified = unqualified
			}
		}
		switch tok {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		case "where":
			if depth == 0 {
				return true
			}
		}
	}
	return len(tokens) > 0
}

var typeModifiers = regexp.MustCompile(`^[0-9]+(,-?[0-9]+)?$`)

// Recognize literal type modifiers only at the start of an ADD COLUMN
// definition. Check any trailing constraints and defaults as expressions.
func backfillColumnDefinition(tokens []string) bool {
	typeWords := 1
	if len(tokens) > 1 && tokens[1] == "varying" {
		switch tokens[0] {
		case "bit", "char", "character":
			typeWords++
		}
	}
	if len(tokens) <= typeWords || tokens[typeWords] != "(" {
		return backfillExpressions(tokens)
	}
	switch tokens[0] {
	case "bit", "char", "character", "varchar", "decimal", "numeric", "time", "timetz", "timestamp", "timestamptz":
		closeIdx := slices.Index(tokens, ")")
		return closeIdx > typeWords+1 &&
			typeModifiers.MatchString(strings.Join(tokens[typeWords+1:closeIdx], "")) &&
			backfillExpressions(tokens[closeIdx+1:])
	default:
		return false
	}
}

// Only simple SQL expressions are eligible. A function, sequence, explicit
// operator, or data-modifying subquery can write outside the new columns.
func backfillExpressions(tokens []string) bool {
	for i, tok := range tokens {
		switch tok {
		case "returning", "with", "select", "table", "insert", "update", "delete", "merge", "truncate",
			"next", "operator", "references", "check", "generated", "serial", "bigserial", "smallserial",
			"serial2", "serial4", "serial8":
			return false
		}
		if i+1 < len(tokens) && tokens[i+1] == "(" && sqlIdentifier(tok) != "" {
			switch tok {
			case "in", "exists", "any", "all", "some", "coalesce", "nullif", "greatest", "least", "row":
				if i == 0 || tokens[i-1] != "." {
					continue
				}
			case "where", "and", "or", "not", "default", "when", "then", "else", "on":
				continue
			}
			return false
		}
	}
	return true
}

// Read sources and targets must be ordinary tables without children or RLS.
// An update target cannot have UPDATE rules, generated columns, or UPDATE
// triggers other than internal FK triggers. Backfills leave existing keys intact.
func (m *Migrator) judgeBackfill(ctx context.Context, tables map[string]*backfillTable) (Verdict, error) {
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		t := tables[name]
		var safe bool
		err := m.pool.DB.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_class c
				WHERE c.oid = to_regclass($1) AND c.relkind = 'r' AND NOT c.relrowsecurity
				AND ($2 = '' OR c.oid = to_regclass(NULLIF($2, '')))
				AND NOT EXISTS (SELECT 1 FROM pg_inherits WHERE inhparent = c.oid)
				AND (NOT $3::boolean OR (
					NOT EXISTS (
						SELECT 1 FROM pg_trigger
						WHERE tgrelid = c.oid AND tgtype & 16 = 16
						AND NOT (tgisinternal AND tgparentid = 0 AND tgconstrrelid <> 0)
					)
					AND NOT EXISTS (SELECT 1 FROM pg_rewrite WHERE ev_class = c.oid AND ev_type = '2')
					AND NOT EXISTS (SELECT 1 FROM pg_attribute
						WHERE attrelid = c.oid AND NOT attisdropped AND attgenerated <> '')
				))
			)`, t.name, t.unqualified, len(t.written) > 0).Scan(&safe)
		if err != nil {
			return Verdict{}, err
		}
		if !safe {
			return Verdict{ErrUndoLossy, "backfill involving " + t.name + " may have side effects"}, nil
		}
	}
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		t := tables[name]
		for _, col := range slices.Sorted(maps.Keys(t.added)) {
			if t.written[col] {
				continue
			}
			why, err := m.downColumnLoses(ctx, t.name, col)
			if err != nil {
				return Verdict{}, err
			}
			if why != "" {
				return Verdict{ErrUndoKeeps, why}, nil
			}
		}
	}
	return Verdict{}, nil
}

var leadingDollarTag = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)

var sqlWord = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9$]*$`)
var sqlToken = regexp.MustCompile(`^(?:[a-zA-Z_][a-zA-Z_0-9$]*|"(?:[^"]|"")*"|'(?:[^']|'')*'|[^[:space:]])`)
var escapeString = regexp.MustCompile(`^[Ee]'(?:[^'\\]|\\[\s\S]|'')*'`)

// Tokens keep quoted identifiers and strings intact, so punctuation or a SQL
// keyword inside either cannot change statement and assignment boundaries.
func undoSQL(script string) [][]string {
	var tokens []string
	for script = strings.TrimSpace(script); script != ""; script = strings.TrimSpace(script) {
		switch {
		case strings.HasPrefix(script, "--"):
			_, script, _ = strings.Cut(script, "\n")
		case strings.HasPrefix(script, "/*"):
			depth := 1
			script = script[2:]
			for depth > 0 && len(script) > 1 {
				switch script[:2] {
				case "/*":
					depth++
					script = script[2:]
				case "*/":
					depth--
					script = script[2:]
				default:
					script = script[1:]
				}
			}
			if depth != 0 {
				return nil
			}
		default:
			if tag := leadingDollarTag.FindStringIndex(script); tag != nil {
				end := strings.Index(script[tag[1]:], script[:tag[1]])
				if end < 0 {
					return nil
				}
				tokens = append(tokens, "''")
				script = script[2*tag[1]+end:]
				continue
			}
			tok := escapeString.FindString(script)
			if tok == "" {
				tok = sqlToken.FindString(script)
			}
			if tok == "" || tok == "'" || tok == `"` {
				return nil
			}
			tokens = append(tokens, tok)
			script = script[len(tok):]
			if sqlWord.MatchString(tok) {
				tokens[len(tokens)-1] = strings.ToLower(tok)
			}
		}
	}
	stmts := splitSQLList(tokens, ";")
	return slices.DeleteFunc(stmts, func(stmt []string) bool { return len(stmt) == 0 })
}

func splitSQLList(tokens []string, separator string) [][]string {
	var list [][]string
	start, depth := 0, 0
	for i, tok := range tokens {
		switch tok {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		case separator:
			if depth == 0 {
				list = append(list, tokens[start:i])
				start = i + 1
			}
		}
	}
	return append(list, tokens[start:])
}

func startsSQL(stmt []string, prefix ...string) bool {
	return len(stmt) >= len(prefix) && slices.Equal(stmt[:len(prefix)], prefix)
}

func sqlIdentifier(token string) string {
	if sqlWord.MatchString(token) {
		return `"` + token + `"`
	}
	if len(token) > 2 && token[0] == '"' {
		return token
	}
	return ""
}

func backfillRelation(stmt []string) (name, unqualified string, rest []string) {
	if len(stmt) == 0 || sqlIdentifier(stmt[0]) == "" {
		return "", "", nil
	}
	first := sqlIdentifier(stmt[0])
	if len(stmt) >= 3 && stmt[1] == "." && sqlIdentifier(stmt[2]) != "" {
		return first + "." + sqlIdentifier(stmt[2]), "", stmt[3:]
	}
	return `"public".` + first, first, stmt[1:]
}
