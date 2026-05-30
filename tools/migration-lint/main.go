// migration-lint enforces the migrations conventions documented in
// shared/db/migrations/README.md.
//
// Checks (Story 22.4 acceptance criteria):
//
//  1. Append-only:    files in shared/db/migrations/ that already exist
//     on the comparison ref (default origin/main) must
//     not be modified or deleted.
//  2. Idempotency:    every CREATE TABLE / CREATE INDEX / DROP TABLE /
//     DROP INDEX / ADD COLUMN uses an IF [NOT] EXISTS
//     guard.
//  3. Long-running:   on Postgres-targeted files, CREATE INDEX must use
//     CONCURRENTLY; ALTER COLUMN ... TYPE / SET NOT NULL
//     patterns are flagged with a fix-it hint.
//  4. SQLite parity:  every NNNN_<topic>.sql ships an
//     NNNN_<topic>.sqlite.sql sibling.
//
// Exit code 0 = clean; non-zero = at least one violation. The output
// is human-readable and lists every violation found (does not bail on
// the first).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func main() {
	var (
		dir     string
		baseRef string
		skipGit bool
	)
	flag.StringVar(&dir, "dir", "shared/db/migrations",
		"Migrations directory (relative to repo root)")
	flag.StringVar(&baseRef, "base-ref", "origin/main",
		"Git ref to compare against for the append-only check")
	flag.BoolVar(&skipGit, "skip-git", false,
		"Skip the append-only check (useful when the base-ref isn't fetched)")
	flag.Parse()

	abs, err := filepath.Abs(dir)
	if err != nil {
		fail("resolve dir: %v", err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		fail("migrations directory %q does not exist", abs)
	}

	violations := 0

	files, err := listMigrations(abs)
	if err != nil {
		fail("list migrations: %v", err)
	}

	// Story 22.4 AC4: a legitimately-safe long-running DDL can be
	// exempted via shared/db/migrations/meta/lints.json. Each exemption
	// is keyed by file basename + rule and carries a mandatory expiry
	// so a stale exemption fails closed instead of lingering forever.
	exemptions, err := loadExemptions(filepath.Join(abs, "meta", "lints.json"))
	if err != nil {
		fail("load lints.json: %v", err)
	}

	if !skipGit {
		if vs, err := appendOnlyCheck(baseRef, dir); err != nil {
			fmt.Fprintf(os.Stderr, "migration-lint: append-only check skipped: %v\n", err)
			fmt.Fprintf(os.Stderr, "  hint: run `git fetch origin main` or pass --skip-git locally\n")
		} else {
			for _, v := range vs {
				report(v)
				violations++
			}
		}
	}

	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			fail("read %s: %v", f, err)
		}
		stripped := stripComments(body)

		dialect := "postgres"
		if strings.HasSuffix(f, ".sqlite.sql") {
			dialect = "sqlite"
		}

		stmts := splitStatements(stripped)
		for _, st := range stmts {
			for _, v := range idempotencyCheck(f, st) {
				violations++
				report(v)
			}
			if dialect == "postgres" {
				for _, v := range longRunningCheck(f, st) {
					if reason, ok := exemptions.exempt(v.File, v.Rule); ok {
						fmt.Fprintf(os.Stderr, "%s: [%s] EXEMPT — %s\n",
							filepath.Base(v.File), v.Rule, reason)
						continue
					}
					violations++
					report(v)
				}
			}
		}
	}

	for _, v := range parityCheck(files) {
		report(v)
		violations++
	}

	if violations > 0 {
		fmt.Fprintf(os.Stderr, "\nmigration-lint: %d violation(s)\n", violations)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "migration-lint: %d files OK\n", len(files))
}

type violation struct {
	File    string
	Rule    string
	Message string
}

func report(v violation) {
	fmt.Fprintf(os.Stderr, "%s: [%s] %s\n", v.File, v.Rule, v.Message)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migration-lint: "+format+"\n", args...)
	os.Exit(2)
}

// ---------------------------------------------------------------------------
// lints.json exemption system (Story 22.4 AC4)
// ---------------------------------------------------------------------------

// exemption is one entry in shared/db/migrations/meta/lints.json. It
// suppresses a single long-running rule for a single migration file
// until `expires` passes; an expired or malformed entry fails closed
// (the violation reappears) so an exemption can never silently outlive
// its justification.
type exemption struct {
	File    string `json:"file"`    // basename, e.g. "0042_backfill.sql"
	Rule    string `json:"rule"`    // e.g. "long-running-set-not-null"
	Reason  string `json:"reason"`  // why this DDL is safe (audit trail)
	Expires string `json:"expires"` // RFC3339 date; required, must be future
}

type exemptionSet struct {
	now   time.Time
	byKey map[string]exemption // "<file>\x00<rule>" -> entry
}

func exemptKey(file, rule string) string {
	return filepath.Base(file) + "\x00" + rule
}

// loadExemptions reads meta/lints.json if present. A missing file is
// not an error (no exemptions). A present-but-malformed file is fatal
// so a typo can't accidentally disable the guard.
func loadExemptions(path string) (*exemptionSet, error) {
	set := &exemptionSet{now: time.Now().UTC(), byKey: map[string]exemption{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, err
	}
	var doc struct {
		Doc        string      `json:"_doc"`
		Exemptions []exemption `json:"exemptions"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, e := range doc.Exemptions {
		if e.File == "" || e.Rule == "" || e.Reason == "" || e.Expires == "" {
			return nil, fmt.Errorf("exemptions[%d]: file, rule, reason, expires are all required", i)
		}
		if _, err := time.Parse(time.RFC3339, e.Expires); err != nil {
			return nil, fmt.Errorf("exemptions[%d].expires %q: not RFC3339: %w", i, e.Expires, err)
		}
		set.byKey[exemptKey(e.File, e.Rule)] = e
	}
	return set, nil
}

// exempt reports whether (file, rule) has a live (non-expired)
// exemption, returning the recorded reason for the audit log line.
func (s *exemptionSet) exempt(file, rule string) (string, bool) {
	e, ok := s.byKey[exemptKey(file, rule)]
	if !ok {
		return "", false
	}
	exp, err := time.Parse(time.RFC3339, e.Expires)
	if err != nil {
		return "", false // unreachable: validated at load
	}
	if !s.now.Before(exp) {
		return "", false // expired -> fails closed, violation reappears
	}
	return e.Reason, true
}

// ---------------------------------------------------------------------------
// listMigrations
// ---------------------------------------------------------------------------

var slotRE = regexp.MustCompile(`^[0-9]{4}_`)

func listMigrations(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".sql" {
			continue
		}
		if !slotRE.MatchString(name) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}

// ---------------------------------------------------------------------------
// Rule 1 — append-only
// ---------------------------------------------------------------------------

// appendOnlyCheck returns one violation per migration file that was
// modified or deleted relative to baseRef inside dir. README.md and
// MANIFEST.md are exempt — they're allowed to evolve.
func appendOnlyCheck(baseRef, dir string) ([]violation, error) {
	cmd := exec.Command("git", "diff", "--name-status", baseRef+"...HEAD", "--", dir)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git diff against %s failed: %s",
				baseRef, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git diff against %s: %w", baseRef, err)
	}

	var violations []violation
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		fields := bytes.SplitN(line, []byte("\t"), 2)
		if len(fields) != 2 {
			continue
		}
		status := string(fields[0])
		path := string(fields[1])
		base := filepath.Base(path)

		if filepath.Ext(base) != ".sql" {
			continue
		}

		switch status[0] {
		case 'A':
			// Added is fine.
		case 'M':
			// Modifications are usually a violation, except when the
			// only diff is inside comments (prose, goose annotations
			// stay verbatim). Comment-only edits don't change the SQL
			// goose runs, so they're safe under the append-only rule.
			same, err := sameAfterStrippingComments(baseRef, path)
			if err != nil {
				// If the comparison itself fails, fall back to strict
				// behaviour so we don't silently allow real diffs.
				violations = append(violations, violation{
					File:    path,
					Rule:    "append-only",
					Message: fmt.Sprintf("file changed (status=M) since %s; couldn't verify comment-only diff (%v) — migrations are append-only", baseRef, err),
				})
				continue
			}
			if same {
				// Comment-only edit; allow.
				continue
			}
			violations = append(violations, violation{
				File:    path,
				Rule:    "append-only",
				Message: fmt.Sprintf("file changed (status=M) since %s; migrations are append-only — add a follow-up slot instead of editing history", baseRef),
			})
		case 'D', 'R', 'C':
			violations = append(violations, violation{
				File:    path,
				Rule:    "append-only",
				Message: fmt.Sprintf("file changed (status=%s) since %s; migrations are append-only — add a follow-up slot instead of editing history", status, baseRef),
			})
		}
	}
	return violations, nil
}

// sameAfterStrippingComments returns true when the path's content at
// baseRef matches the content at HEAD with all `--`-prefixed comment
// lines stripped. Lets the append-only rule allow comment-only edits
// (prose tweaks, reworded headers) while still catching real SQL
// changes after the migration has been applied to production DBs.
//
// Both versions come from `git show <ref>:<path>` so the comparison
// doesn't depend on the linter's cwd (the Makefile runs it from
// `tools/migration-lint/`, but git diff paths are repo-relative).
func sameAfterStrippingComments(baseRef, path string) (bool, error) {
	baseBody, err := gitShow(baseRef, path)
	if err != nil {
		return false, err
	}
	currentBody, err := gitShow("HEAD", path)
	if err != nil {
		return false, err
	}
	return normalizeForComparison(baseBody) == normalizeForComparison(currentBody), nil
}

// normalizeForComparison strips comments and collapses whitespace so a
// comment-only edit (which `stripComments` reduces to a different
// number of blank lines than the base) compares equal to the base.
func normalizeForComparison(body []byte) string {
	stripped := stripComments(body)
	fields := strings.Fields(string(stripped))
	return strings.Join(fields, " ")
}

func gitShow(ref, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+path)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git show %s:%s failed: %s",
				ref, path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Rule 2 — idempotency
// ---------------------------------------------------------------------------

var (
	reCreateTable    = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\b`)
	reCreateTableOK  = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\b`)
	reCreateIndex    = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\b`)
	reCreateIndexOK  = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?IF\s+NOT\s+EXISTS\b`)
	reDropTable      = regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)
	reDropTableOK    = regexp.MustCompile(`(?i)\bDROP\s+TABLE\s+IF\s+EXISTS\b`)
	reDropIndex      = regexp.MustCompile(`(?i)\bDROP\s+INDEX\b`)
	reDropIndexOK    = regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+IF\s+EXISTS\b`)
	reAddColumn      = regexp.MustCompile(`(?i)\bADD\s+COLUMN\b`)
	reAddColumnOK    = regexp.MustCompile(`(?i)\bADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\b`)
	reCreateView     = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?VIEW\b`)
	reCreateViewOK   = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+VIEW|VIEW\s+IF\s+NOT\s+EXISTS)\b`)
	reCreateTrigger  = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\b`)
	reCreateTriggerO = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+TRIGGER|TRIGGER\s+IF\s+NOT\s+EXISTS)\b`)
)

func idempotencyCheck(file string, stmt string) []violation {
	var out []violation
	check := func(rule, hint string, bad, good *regexp.Regexp) {
		if bad.MatchString(stmt) && !good.MatchString(stmt) {
			out = append(out, violation{
				File:    file,
				Rule:    rule,
				Message: fmt.Sprintf("%s — statement: %q", hint, oneLine(stmt, 100)),
			})
		}
	}
	check("idempotent-create-table",
		"unguarded `CREATE TABLE`; use `CREATE TABLE IF NOT EXISTS …`",
		reCreateTable, reCreateTableOK)
	check("idempotent-create-index",
		"unguarded `CREATE INDEX`; use `CREATE INDEX IF NOT EXISTS …` (Postgres: prefer `CREATE INDEX CONCURRENTLY IF NOT EXISTS …`)",
		reCreateIndex, reCreateIndexOK)
	check("idempotent-drop-table",
		"unguarded `DROP TABLE`; use `DROP TABLE IF EXISTS …`",
		reDropTable, reDropTableOK)
	check("idempotent-drop-index",
		"unguarded `DROP INDEX`; use `DROP INDEX IF EXISTS …`",
		reDropIndex, reDropIndexOK)
	check("idempotent-add-column",
		"unguarded `ADD COLUMN`; use `ALTER TABLE … ADD COLUMN IF NOT EXISTS …`",
		reAddColumn, reAddColumnOK)
	check("idempotent-create-view",
		"unguarded `CREATE VIEW`; use `CREATE OR REPLACE VIEW …` (Postgres) or `CREATE VIEW IF NOT EXISTS …` (SQLite)",
		reCreateView, reCreateViewOK)
	check("idempotent-create-trigger",
		"unguarded `CREATE TRIGGER`; use `CREATE OR REPLACE TRIGGER …` or `CREATE TRIGGER IF NOT EXISTS …`",
		reCreateTrigger, reCreateTriggerO)
	return out
}

// ---------------------------------------------------------------------------
// Rule 3 — long-running DDL on Postgres
// ---------------------------------------------------------------------------

var (
	reLongCreateIndex   = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\b`)
	reLongCreateIndexOK = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\b`)
	reLongAlterType     = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+ALTER\s+COLUMN\s+\S+\s+(?:SET\s+DATA\s+)?TYPE\b`)
	reLongSetNotNull    = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+ALTER\s+COLUMN\s+\S+\s+SET\s+NOT\s+NULL\b`)
)

func longRunningCheck(file string, stmt string) []violation {
	var out []violation
	if reLongCreateIndex.MatchString(stmt) && !reLongCreateIndexOK.MatchString(stmt) {
		out = append(out, violation{
			File:    file,
			Rule:    "long-running-create-index",
			Message: fmt.Sprintf("Postgres `CREATE INDEX` takes ACCESS EXCLUSIVE; use `CREATE INDEX CONCURRENTLY` (and `IF NOT EXISTS`) — see migrations/README.md §4. Statement: %q", oneLine(stmt, 100)),
		})
	}
	if reLongAlterType.MatchString(stmt) {
		out = append(out, violation{
			File:    file,
			Rule:    "long-running-alter-type",
			Message: fmt.Sprintf("`ALTER COLUMN ... TYPE` rewrites the table; ship new column + backfill job + swap migration instead. Statement: %q", oneLine(stmt, 100)),
		})
	}
	if reLongSetNotNull.MatchString(stmt) {
		out = append(out, violation{
			File:    file,
			Rule:    "long-running-set-not-null",
			Message: fmt.Sprintf("`SET NOT NULL` scans the whole table; use `NOT VALID` constraint + async validate instead. Statement: %q", oneLine(stmt, 100)),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Rule 4 — SQLite parity
// ---------------------------------------------------------------------------

func parityCheck(files []string) []violation {
	var out []violation
	for _, f := range files {
		if strings.HasSuffix(f, ".sqlite.sql") {
			continue
		}
		sibling := strings.TrimSuffix(f, ".sql") + ".sqlite.sql"
		if _, err := os.Stat(sibling); err != nil {
			out = append(out, violation{
				File:    f,
				Rule:    "sqlite-parity",
				Message: fmt.Sprintf("missing SQLite sibling %s — every NNNN_*.sql ships a NNNN_*.sqlite.sql parity file (see migrations/README.md §3)", filepath.Base(sibling)),
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stripComments removes single-line `-- ...` comments (including
// goose's `-- +goose ...` directives) so the regex matchers don't
// false-positive on directive text.
func stripComments(body []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(body, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if bytes.HasPrefix(trimmed, []byte("--")) {
			out.WriteByte('\n')
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// splitStatements is a *naive* `;`-splitter: it ignores semicolons
// inside single-quoted strings or `$$ ... $$` quoted blocks, which is
// enough for goose-style migrations where each statement sits inside
// `+goose StatementBegin/End` pairs.
func splitStatements(body []byte) []string {
	var (
		out   []string
		buf   bytes.Buffer
		inStr bool
		inDol bool
	)
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case inStr:
			buf.WriteByte(c)
			if c == '\'' {
				inStr = false
			}
		case inDol:
			buf.WriteByte(c)
			if c == '$' && i+1 < len(body) && body[i+1] == '$' {
				buf.WriteByte('$')
				i++
				inDol = false
			}
		default:
			if c == '\'' {
				buf.WriteByte(c)
				inStr = true
				continue
			}
			if c == '$' && i+1 < len(body) && body[i+1] == '$' {
				buf.WriteByte('$')
				buf.WriteByte('$')
				i++
				inDol = true
				continue
			}
			if c == ';' {
				stmt := strings.TrimSpace(buf.String())
				if stmt != "" {
					out = append(out, stmt)
				}
				buf.Reset()
				continue
			}
			buf.WriteByte(c)
		}
	}
	if rest := strings.TrimSpace(buf.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
