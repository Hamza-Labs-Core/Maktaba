package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp writes body to a fresh temp file and returns its path.
func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "lints.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func TestIdempotencyCatchesUnguardedCreateTable(t *testing.T) {
	stmt := "CREATE TABLE foo (id int)"
	vs := idempotencyCheck("0099_test.sql", stmt)
	if len(vs) == 0 {
		t.Fatal("expected violation for unguarded CREATE TABLE")
	}
	if vs[0].Rule != "idempotent-create-table" {
		t.Errorf("rule = %q, want idempotent-create-table", vs[0].Rule)
	}
}

func TestIdempotencyAcceptsGuarded(t *testing.T) {
	cases := []string{
		"CREATE TABLE IF NOT EXISTS foo (id int)",
		"CREATE INDEX IF NOT EXISTS idx ON foo(id)",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx ON foo(id)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx ON foo(id)",
		"DROP TABLE IF EXISTS foo",
		"DROP INDEX IF EXISTS idx",
		"ALTER TABLE foo ADD COLUMN IF NOT EXISTS bar int",
	}
	for _, stmt := range cases {
		t.Run(stmt, func(t *testing.T) {
			if vs := idempotencyCheck("x.sql", stmt); len(vs) > 0 {
				t.Errorf("expected clean, got %d violations: %v", len(vs), vs)
			}
		})
	}
}

func TestIdempotencyCatchesEachKind(t *testing.T) {
	cases := map[string]string{
		"idempotent-create-table": "CREATE TABLE foo (id int)",
		"idempotent-create-index": "CREATE INDEX idx ON foo(id)",
		"idempotent-drop-table":   "DROP TABLE foo",
		"idempotent-drop-index":   "DROP INDEX idx",
		"idempotent-add-column":   "ALTER TABLE foo ADD COLUMN bar int",
	}
	for wantRule, stmt := range cases {
		t.Run(wantRule, func(t *testing.T) {
			vs := idempotencyCheck("x.sql", stmt)
			if len(vs) == 0 {
				t.Fatalf("expected violation, got none for %q", stmt)
			}
			found := false
			for _, v := range vs {
				if v.Rule == wantRule {
					found = true
				}
			}
			if !found {
				t.Errorf("did not see rule %s; saw: %v", wantRule, vs)
			}
		})
	}
}

func TestLongRunningCatchesPlainCreateIndex(t *testing.T) {
	stmt := "CREATE INDEX IF NOT EXISTS idx ON videos(name)"
	vs := longRunningCheck("0099_test.sql", stmt)
	if len(vs) == 0 {
		t.Fatal("expected violation for non-CONCURRENTLY CREATE INDEX")
	}
	if vs[0].Rule != "long-running-create-index" {
		t.Errorf("rule = %q, want long-running-create-index", vs[0].Rule)
	}
}

func TestLongRunningAcceptsConcurrently(t *testing.T) {
	stmt := "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx ON videos(name)"
	if vs := longRunningCheck("x.sql", stmt); len(vs) > 0 {
		t.Errorf("expected clean, got %v", vs)
	}
}

func TestLongRunningCatchesAlterType(t *testing.T) {
	stmt := "ALTER TABLE foo ALTER COLUMN bar TYPE bigint"
	vs := longRunningCheck("x.sql", stmt)
	if len(vs) == 0 || vs[0].Rule != "long-running-alter-type" {
		t.Fatalf("expected long-running-alter-type, got %v", vs)
	}
}

func TestLongRunningCatchesSetNotNull(t *testing.T) {
	stmt := "ALTER TABLE foo ALTER COLUMN bar SET NOT NULL"
	vs := longRunningCheck("x.sql", stmt)
	if len(vs) == 0 || vs[0].Rule != "long-running-set-not-null" {
		t.Fatalf("expected long-running-set-not-null, got %v", vs)
	}
}

func TestSplitStatementsHandlesGooseBlocks(t *testing.T) {
	body := []byte(`
CREATE TABLE IF NOT EXISTS a (id int);
CREATE TABLE IF NOT EXISTS b (id int);
INSERT INTO a (id) VALUES (1);
`)
	stmts := splitStatements(body)
	if len(stmts) != 3 {
		t.Fatalf("got %d stmts, want 3: %#v", len(stmts), stmts)
	}
}

func TestSplitStatementsIgnoresSemicolonInString(t *testing.T) {
	body := []byte(`INSERT INTO a (note) VALUES ('a;b'); SELECT 1;`)
	stmts := splitStatements(body)
	if len(stmts) != 2 {
		t.Fatalf("got %d stmts, want 2: %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "'a;b'") {
		t.Errorf("first stmt lost the embedded semicolon: %q", stmts[0])
	}
}

func TestStripCommentsRemovesGooseDirectives(t *testing.T) {
	body := []byte(`-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS foo (id int);
-- +goose StatementEnd`)
	stripped := stripComments(body)
	if strings.Contains(string(stripped), "+goose") {
		t.Errorf("goose directive survived: %q", stripped)
	}
	if !strings.Contains(string(stripped), "CREATE TABLE") {
		t.Errorf("CREATE TABLE got dropped: %q", stripped)
	}
}

// --- lints.json exemption system (Story 22.4 AC4) ---

func TestLoadExemptions_MissingFileIsNoError(t *testing.T) {
	set, err := loadExemptions(t.TempDir() + "/does-not-exist.json")
	if err != nil {
		t.Fatalf("missing file: want nil error, got %v", err)
	}
	if _, ok := set.exempt("0042_x.sql", "long-running-set-not-null"); ok {
		t.Errorf("empty set should exempt nothing")
	}
}

func TestLoadExemptions_LiveExemptionApplies(t *testing.T) {
	p := writeTemp(t, `{"exemptions":[{"file":"0042_x.sql","rule":"long-running-set-not-null","reason":"backfilled async; column already NOT NULL in practice","expires":"2999-01-01T00:00:00Z"}]}`)
	set, err := loadExemptions(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	reason, ok := set.exempt("shared/db/migrations/0042_x.sql", "long-running-set-not-null")
	if !ok {
		t.Fatalf("live exemption should apply")
	}
	if !strings.Contains(reason, "backfilled async") {
		t.Errorf("reason not surfaced: %q", reason)
	}
	if _, ok := set.exempt("0042_x.sql", "long-running-create-index"); ok {
		t.Errorf("exemption must be rule-scoped")
	}
}

func TestLoadExemptions_ExpiredFailsClosed(t *testing.T) {
	p := writeTemp(t, `{"exemptions":[{"file":"0042_x.sql","rule":"long-running-alter-type","reason":"r","expires":"2000-01-01T00:00:00Z"}]}`)
	set, err := loadExemptions(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := set.exempt("0042_x.sql", "long-running-alter-type"); ok {
		t.Errorf("expired exemption must fail closed (violation reappears)")
	}
}

func TestLoadExemptions_MalformedIsFatal(t *testing.T) {
	for name, body := range map[string]string{
		"missing reason": `{"exemptions":[{"file":"a.sql","rule":"r","expires":"2999-01-01T00:00:00Z"}]}`,
		"bad expiry":     `{"exemptions":[{"file":"a.sql","rule":"r","reason":"x","expires":"soon"}]}`,
		"unknown field":  `{"exemptions":[{"file":"a.sql","rule":"r","reason":"x","expires":"2999-01-01T00:00:00Z","oops":1}]}`,
		"not json":       `{`,
	} {
		p := writeTemp(t, body)
		if _, err := loadExemptions(p); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
